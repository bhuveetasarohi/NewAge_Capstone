"""Recommendation worker: counts which products are bought together.

For every order, each pair of distinct products gets its co-purchase counter incremented.
The Go product service reads these counters to build the "For you" list.
"""
import logging

from db import pool

log = logging.getLogger("recommendation")

UPSERT_PAIR = """
    INSERT INTO product_copurchases (product_id, related_product_id, times_bought_together)
    VALUES (%s, %s, 1)
    ON CONFLICT (product_id, related_product_id)
    DO UPDATE SET times_bought_together = product_copurchases.times_bought_together + 1
"""


async def handle(event: dict) -> None:
    product_ids = sorted({int(item["product_id"]) for item in event["items"]})
    pairs = [(a, b) for a in product_ids for b in product_ids if a != b]
    if not pairs:
        return

    # Sorted pairs keep lock acquisition order consistent across concurrent orders.
    pairs.sort()
    async with pool.connection() as conn:  # commits when the block exits cleanly
        async with conn.cursor() as cur:
            await cur.executemany(UPSERT_PAIR, pairs)
    log.info("updated %d co-purchase pair(s) for order %s", len(pairs), event["order_id"])
