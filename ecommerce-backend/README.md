# E-Commerce Backend System

A distributed, microservice-based e-commerce backend with a working web UI. Go handles the
synchronous, transactional path (auth, cart, inventory, checkout). Python handles the
asynchronous background work (PDF invoices, notifications, recommendations). The two
halves are decoupled by Redis Pub/Sub. This is the implementation of the ENSP456 Capstone
(K.R. Mangalam University, Phase I design: *E-Commerce Backend System*).

```
                         ┌──────────────────────────── Browser (ShopSphere UI) ────────────────────────────┐
                         │                                                                                │
                         ▼                                                                                │
              ┌──────────────────────┐   static UI + /api/*  (JWT check on protected routes)                 │
              │   Go API Gateway     │ ──────────────────────────────────────────────────────────────────────┘
              │   :8080 (host)       │
              └───┬────────┬────────┬───────────────┬───────────────────────────────────┐
                  │        │        │               │                                   │
                  ▼        ▼        ▼               ▼                                   │
            ┌────────┐┌─────────┐┌───────────┐┌───────────────┐                          │
            │ User   ││ Product ││ Inventory ││ Order         │  checkout = one DB       │
            │ (Go)   ││ (Go)    ││ (Go)      ││ (Go): cart,   │  transaction:            │
            │        ││         ││           ││ checkout,     │  lock cart → reserve     │
            │        ││         ││           ││ invoices      │  stock → insert order    │
            └───┬────┘└────┬────┘└─────┬─────┘└──────┬────────┘  → commit               │
                │          │           │             │                                   │
                └──────────┴─────┬─────┴─────────────┘                                   │
                                 ▼                                                       │
                       ┌───────────────────┐                                             │
                       │ PostgreSQL 16     │                                             │
                       └───────────────────┘                                             │
                                                                                         │
   Order service ── publishes OrderCreated after commit ──►  ┌───────────────────┐         │
                                                            │ Redis Pub/Sub     │         │
                                                            │ channel:          │         │
                                                            │ order_events      │         │
                                                            └─────────┬─────────┘         │
                                                                      │ subscribe         │
                                                                      ▼                   │
                                                  ┌────────────────────────────────────┐  │
                                                  │ Python worker (asyncio)            │  │
                                                  │  ├─ invoice:        PDF (ReportLab)│──┘ writes order_<id>.pdf
                                                  │  ├─ notification:   log row + SMTP │     (shared volume, read by Order svc)
                                                  │  └─ recommendation: co-purchases   │
                                                  └────────────────────────────────────┘
```

## Phase I objectives → where they are implemented

| Report objective | Implementation |
|---|---|
| RESTful Go services for user auth, catalogue, inventory, orders | `services/cmd/{user,product,inventory,order}` |
| Handle at least 50 concurrent clients | Tested with 50 concurrent shoppers, see [Tests](#tests) |
| Event-driven checkout, OrderCreated published to Redis | `services/internal/order/handler.go` (`checkout`), `services/internal/common/events.go` |
| Python services consume order events asynchronously | `worker/main.py` and `worker/handlers/` |
| PDF invoice generation (Python) | `worker/handlers/invoice.py` (ReportLab) |
| Order-confirmation notification (Python) | `worker/handlers/notification.py` (DB log, optional SMTP) |
| Basic product recommendation from purchased products | `worker/handlers/recommendation.py` + `GET /api/recommendations` |
| JWT-based login | `services/internal/common/auth.go` (HS256, standard library only) |
| Stock never oversold under concurrent checkouts | Conditional `UPDATE … WHERE stock_quantity >= $n` inside the checkout transaction |
| REST API documentation and Docker-based deployment | [API reference](#api-reference) below, `docker-compose.yml` |

The report lists a frontend as out of scope. The UI in `frontend/` is an addition, so the
team can demonstrate the system end to end. It contains no business logic of its own, and
you can delete the folder without affecting the backend.

## Quick start (Docker)

Requirements: Docker with Compose v2.

```bash
cp .env.example .env          # optional: change the secrets
docker compose up --build     # first build takes a few minutes
```

Open **http://localhost:8080** and register an account. The `docker compose` project creates
the schema and seed data on first start.

Stop with `Ctrl+C`, or `docker compose down`. To reset the database and invoices, use
`docker compose down -v`.

## Quick start (without Docker)

Requirements: Go 1.24+, Python 3.10+, PostgreSQL 14+, Redis 6+.

```bash
# 1. Python worker dependencies
python3 -m pip install -r worker/requirements.txt

# 2. Create the database and load the schema + seed data
createdb ecommerce
psql "postgres://postgres:postgres@localhost:5432/ecommerce" -f db/init.sql

# 3. Start everything (Go services, gateway, Python worker)
scripts/run_local.sh
```

Open **http://localhost:8000**. Logs go to `logs/`. Press `Ctrl+C` to stop all processes.

## Using the UI

| Page | What it demonstrates |
|---|---|
| **Shop** | Catalogue from the product service. Search, category filter, live stock badges. Out-of-stock items disable the button. |
| **Cart** | Quantity changes and removal. Quantities are capped at available stock. |
| **Place order** | Synchronous checkout: stock is reserved and the order is committed before the response returns. |
| **Orders** | Order history. "Download invoice" fetches the PDF that the Python worker produced. |
| **Notifications** | Rows written by the Python notification worker. |
| **For you** | Recommendations from co-purchase counts built by the Python recommendation worker. |
| **Admin** | Create products and set stock levels. |

Seed data includes a low-stock product (Smart Watch, 3 left) and an out-of-stock product
(LED Desk Lamp) to show those edge cases. Prices are in rupees.

## API reference

All endpoints are served through the gateway under `/api`. Protected endpoints need the
header `Authorization: Bearer <token>`, which you get from register or login. Errors are
returned as `{"error": "..."}`.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | `/api/auth/register` | — | Create an account. Returns `{token, user}`. |
| POST | `/api/auth/login` | — | Sign in. Returns `{token, user}`. |
| GET | `/api/users/me` | ✔ | Current profile. |
| GET | `/api/products?category=&q=` | — | List products with stock. |
| GET | `/api/products/{id}` | — | One product. |
| GET | `/api/categories` | — | Distinct categories. |
| POST | `/api/products` | ✔ | Create a product and its opening stock. |
| PUT | `/api/products/{id}` | ✔ | Update product details (not stock). |
| GET | `/api/inventory` | — | Stock levels for all products. |
| GET | `/api/inventory/{productId}` | — | Stock level for one product. |
| PUT | `/api/inventory/{productId}` | ✔ | Set stock: `{"stock_quantity": 10}`. |
| GET | `/api/cart` | ✔ | Cart with totals. |
| POST | `/api/cart` | ✔ | Add: `{"product_id": 1, "quantity": 2}`. |
| PUT | `/api/cart/{productId}` | ✔ | Set quantity. `0` removes the line. |
| DELETE | `/api/cart/{productId}` | ✔ | Remove a line. |
| POST | `/api/checkout` | ✔ | Place the order. Returns `201` with the order. `409` if stock ran out. |
| GET | `/api/orders` | ✔ | Order history. |
| GET | `/api/orders/{id}` | ✔ | One order. |
| GET | `/api/orders/{id}/invoice` | ✔ | PDF. Returns `202` while the worker is still rendering it. |
| GET | `/api/notifications` | ✔ | Notifications for the current user. |
| GET | `/api/recommendations` | ✔ | Products often bought with the user's past purchases. |
| GET | `/health` | — | Liveness. Available on the gateway and on each service. |

Example:

```bash
TOKEN=$(curl -s -X POST localhost:8080/api/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","password":"password123"}' | jq -r .token)

curl -s -X POST localhost:8080/api/cart -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"product_id":1,"quantity":1}'
curl -s -X POST localhost:8080/api/checkout -H "Authorization: Bearer $TOKEN"
```

## Tests

With the stack running (Docker or local), from the repository root:

```bash
# Functional smoke test: registration, auth, cart, checkout, history, error paths
BASE_URL=http://localhost:8080 python3 tests/smoke_test.py

# Concurrency: 50 shoppers race for 20 units, then a 200-order throughput run
BASE_URL=http://localhost:8080 python3 tests/concurrency_check.py
```

The tests use only the Python standard library. Local runs use port 8000. Set `BASE_URL`
to match.

The concurrency test creates products in a `load-test` category, so they show up in the
catalogue. Reset with `docker compose down -v` if you want a clean catalogue.

Results from the verification run (single laptop-class machine, native processes):

- 50 concurrent checkouts for 20 units → exactly 20 orders, 30 × `409`, final stock `0`.
- 200 checkouts across 50 shoppers → all succeeded, about 165 orders/sec.
- Every order produced an invoice PDF and a notification row, with zero worker errors.

## Project layout

```
ecommerce-backend/
├── docker-compose.yml          # Postgres, Redis, 5 Go services, Python worker
├── .env.example
├── db/init.sql                 # schema + seed data (idempotent)
├── services/                   # Go module
│   ├── Dockerfile              # one image per service (build arg SERVICE)
│   ├── cmd/                    # main packages: gateway, user, product, inventory, order
│   └── internal/
│       ├── common/             # config, DB pool, HTTP helpers, JWT, password hashing, events
│       ├── user/               # register, login, profile, notifications
│       ├── product/            # catalogue + recommendations
│       ├── inventory/          # stock levels + Reserve() used by checkout
│       ├── order/              # cart, checkout, orders, invoice download
│       └── gateway/            # reverse proxy, auth check, static UI
├── worker/                     # Python 3.12 asyncio consumer
│   ├── main.py                 # Redis subscriber, dispatches each event to handlers
│   ├── handlers/invoice.py     # ReportLab PDF
│   ├── handlers/notification.py
│   └── handlers/recommendation.py
├── frontend/                   # ShopSphere UI: plain HTML/CSS/JS, no build step
├── tests/                      # smoke test and concurrency check
└── scripts/run_local.sh        # run everything natively
```

## Design decisions and limitations

These are deliberate trade-offs. Several match the report's scope. Others are known gaps.

- **Trust model.** Only the gateway is exposed to the host. It validates tokens and forwards
  requests. Each service also verifies the token itself with the shared `JWT_SECRET`, so a
  request that reaches a service directly is still checked.
- **Inventory logic is shared with checkout.** `inventory.Reserve` runs inside the order
  service's transaction and uses the same `inventory` table. Both services use one
  PostgreSQL database, so this keeps the overselling guarantee simple: one transaction, one
  set of row locks. A fully separate inventory service would need a saga with compensating
  actions. That is possible future work, but it is more complexity than this scope needs.
- **Redis Pub/Sub is fire-and-forget.** If the worker is down when an order is placed, that
  event is lost, so the invoice and notification for that order will not be created. The
  order itself is safe. The planned fix is a transactional outbox table that the publisher
  drains. The report's Phase I design accepts this trade-off.
- **Publish happens after commit.** If Redis is unreachable at checkout, the order still
  succeeds and a warning is logged. Background work is skipped for that order.
- **Payments are simulated**, as the report specifies. Orders are stored as
  `PAID_SIMULATED` and no payment gateway is called.
- **Passwords** use PBKDF2-SHA256 with 600,000 iterations from the Go standard library. The
  JWT implementation is a small HS256 verifier and signer, also standard-library only. It
  is intentionally narrow: it accepts only the one header it issues.
- **Recommendations** count co-purchases from order history. They are not ML, as the report
  specifies.
- **Email** is optional. Set `SMTP_HOST` and the worker sends a confirmation. Otherwise the
  notification is written to the database only.
- **No admin roles.** Any signed-in user can use the Admin page. A real deployment needs an
  admin role check on product and inventory writes.
- **Invoices** are stored on a shared volume. The Order service serves them, and only the
  worker can write them.

## Development notes

- Go dependencies: `github.com/lib/pq` and `github.com/redis/go-redis/v9`, pinned to versions
  that build on Go 1.24. Run `go mod tidy` inside `services/` after changing imports.
- Python dependencies are pinned by range in `worker/requirements.txt`.
- The schema is applied only on the first start of an empty Postgres volume. Changes to
  `db/init.sql` need `docker compose down -v`, or the same SQL run by hand.
