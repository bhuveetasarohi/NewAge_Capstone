"""Background worker entry point.

Subscribes to the Redis channel that the Go order service publishes to. For each
OrderCreated event, the three handlers (invoice, notification, recommendation) run
concurrently. An error in one handler is logged and does not affect the others.
"""
import asyncio
import json
import logging
import signal

import redis.asyncio as aioredis

import db
import settings
from handlers import invoice, notification, recommendation

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [worker] %(levelname)s %(name)s: %(message)s",
)
log = logging.getLogger("worker")

HANDLERS = (
    ("invoice", invoice.handle),
    ("notification", notification.handle),
    ("recommendation", recommendation.handle),
)

# Strong references keep in-flight tasks from being garbage-collected.
_tasks: set[asyncio.Task] = set()


async def _run_handler(name: str, handler, event: dict) -> None:
    try:
        await handler(event)
    except Exception:  # one failing consumer must not stop the others
        log.exception("handler %r failed for order %s", name, event.get("order_id"))


async def process(event: dict) -> None:
    await asyncio.gather(*(_run_handler(name, fn, event) for name, fn in HANDLERS))


async def _listen_once() -> None:
    client = aioredis.from_url(settings.REDIS_URL, decode_responses=True)
    pubsub = client.pubsub(ignore_subscribe_messages=True)
    try:
        await pubsub.subscribe(settings.ORDER_EVENTS_CHANNEL)
        log.info("subscribed to channel %r at %s", settings.ORDER_EVENTS_CHANNEL, settings.REDIS_URL)
        async for message in pubsub.listen():
            if message.get("type") != "message":
                continue
            try:
                event = json.loads(message["data"])
            except (TypeError, json.JSONDecodeError):
                log.error("discarding malformed event: %.200r", message.get("data"))
                continue
            if event.get("type") != "OrderCreated":
                continue
            log.info("received OrderCreated for order %s", event.get("order_id"))
            task = asyncio.create_task(process(event))
            _tasks.add(task)
            task.add_done_callback(_tasks.discard)
    finally:
        try:
            await pubsub.aclose()
            await client.aclose()
        except Exception:
            pass


async def consume() -> None:
    while True:
        try:
            await _listen_once()
        except asyncio.CancelledError:
            raise
        except Exception as exc:  # Redis unreachable or connection dropped
            log.warning("redis connection problem (%s); retrying in 2s", exc)
        await asyncio.sleep(2)


async def main() -> None:
    await db.pool.open(wait=True, timeout=60)  # fail fast if Postgres never becomes reachable

    loop = asyncio.get_running_loop()
    stop = asyncio.Event()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, stop.set)

    consumer = asyncio.create_task(consume())
    log.info("worker started; waiting for OrderCreated events")
    await stop.wait()

    log.info("shutting down")
    consumer.cancel()
    await asyncio.gather(consumer, return_exceptions=True)
    if _tasks:
        await asyncio.gather(*_tasks, return_exceptions=True)
    await db.pool.close()


if __name__ == "__main__":
    asyncio.run(main())
