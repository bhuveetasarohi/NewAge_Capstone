"""Notification worker: records an order confirmation and optionally emails it."""
import asyncio
import logging
import smtplib
from email.message import EmailMessage

from db import pool
from settings import SMTP_FROM, SMTP_HOST, SMTP_PASSWORD, SMTP_PORT, SMTP_USER

log = logging.getLogger("notification")


def _send_email(to_addr: str, subject: str, body: str) -> None:
    """Blocking SMTP send. Called from a thread so it never stalls the event loop."""
    msg = EmailMessage()
    msg["Subject"] = subject
    msg["From"] = SMTP_FROM
    msg["To"] = to_addr
    msg.set_content(body)
    with smtplib.SMTP(SMTP_HOST, SMTP_PORT, timeout=15) as smtp:
        smtp.starttls()
        if SMTP_USER:
            smtp.login(SMTP_USER, SMTP_PASSWORD)
        smtp.send_message(msg)


async def handle(event: dict) -> None:
    order_id = int(event["order_id"])
    user_id = int(event["user_id"])
    total = float(event["total_amount"])

    message = f"Order #{order_id} confirmed. Rs. {total:,.2f} received (simulated payment)."
    async with pool.connection() as conn:
        await conn.execute(
            "INSERT INTO notifications (user_id, order_id, message) VALUES (%s, %s, %s)",
            (user_id, order_id, message),
        )
    log.info("notification recorded for user %s: %s", user_id, message)

    if not SMTP_HOST:
        log.info("SMTP_HOST not set; confirmation email skipped for order %s", order_id)
        return

    body = (
        f"Hi {event['user_name']},\n\n"
        f"Thanks for your order #{order_id}. Total paid: Rs. {total:,.2f}.\n"
        "Your invoice is available on the Orders page.\n"
    )
    await asyncio.to_thread(_send_email, event["user_email"], f"Order #{order_id} confirmed", body)
    log.info("confirmation email sent to %s", event["user_email"])
