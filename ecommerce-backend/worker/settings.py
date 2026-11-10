"""Runtime configuration, read from environment variables."""
import os

REDIS_URL = os.getenv("REDIS_URL", "redis://localhost:6379/0")
DATABASE_URL = os.getenv(
    "DATABASE_URL", "postgresql://postgres:postgres@localhost:5432/ecommerce?sslmode=disable"
)
ORDER_EVENTS_CHANNEL = os.getenv("ORDER_EVENTS_CHANNEL", "order_events")
INVOICE_DIR = os.getenv("INVOICE_DIR", "./invoices")

# Email is optional. When SMTP_HOST is empty the notification handler only logs to the DB.
SMTP_HOST = os.getenv("SMTP_HOST", "")
SMTP_PORT = int(os.getenv("SMTP_PORT", "587"))
SMTP_USER = os.getenv("SMTP_USER", "")
SMTP_PASSWORD = os.getenv("SMTP_PASSWORD", "")
SMTP_FROM = os.getenv("SMTP_FROM", "shop@example.com")
