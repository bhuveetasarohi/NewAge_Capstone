"""PostgreSQL access for the workers.

One connection pool is shared by all handlers. Opening a connection per event would
exhaust Postgres's connection limit as soon as a burst of orders arrives.
"""
from psycopg_pool import AsyncConnectionPool

from settings import DATABASE_URL

POOL_MIN_SIZE = 1
POOL_MAX_SIZE = 10

pool = AsyncConnectionPool(
    conninfo=DATABASE_URL,
    min_size=POOL_MIN_SIZE,
    max_size=POOL_MAX_SIZE,
    open=False,  # opened explicitly in main(), so startup failures are visible
)
