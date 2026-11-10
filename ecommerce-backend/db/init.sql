-- E-Commerce Backend System: PostgreSQL schema and seed data.
-- Safe to run more than once (all statements are idempotent).

CREATE TABLE IF NOT EXISTS users (
    id            SERIAL PRIMARY KEY,
    name          VARCHAR(100) NOT NULL,
    email         VARCHAR(255) NOT NULL UNIQUE,
    password_hash VARCHAR(255) NOT NULL,
    created_at    TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS products (
    id          SERIAL PRIMARY KEY,
    name        VARCHAR(150) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    price       NUMERIC(10,2) NOT NULL CHECK (price >= 0),
    category    VARCHAR(50) NOT NULL,
    image_emoji VARCHAR(16) NOT NULL DEFAULT '📦',
    created_at  TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Stock lives in its own table so the inventory service owns it.
CREATE TABLE IF NOT EXISTS inventory (
    product_id     INT PRIMARY KEY REFERENCES products(id) ON DELETE CASCADE,
    stock_quantity INT NOT NULL CHECK (stock_quantity >= 0),
    updated_at     TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS cart_items (
    user_id    INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    product_id INT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    quantity   INT NOT NULL CHECK (quantity > 0),
    PRIMARY KEY (user_id, product_id)
);

CREATE TABLE IF NOT EXISTS orders (
    id             SERIAL PRIMARY KEY,
    user_id        INT NOT NULL REFERENCES users(id),
    total_amount   NUMERIC(10,2) NOT NULL,
    status         VARCHAR(20) NOT NULL,
    payment_status VARCHAR(20) NOT NULL,
    created_at     TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS order_items (
    id           SERIAL PRIMARY KEY,
    order_id     INT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    product_id   INT NOT NULL REFERENCES products(id),
    product_name VARCHAR(150) NOT NULL,
    category     VARCHAR(50) NOT NULL,
    quantity     INT NOT NULL CHECK (quantity > 0),
    unit_price   NUMERIC(10,2) NOT NULL
);

-- Written by the Python notification worker.
CREATE TABLE IF NOT EXISTS notifications (
    id         SERIAL PRIMARY KEY,
    user_id    INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    order_id   INT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    message    TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT NOW()
);

-- Written by the Python recommendation worker.
CREATE TABLE IF NOT EXISTS product_copurchases (
    product_id            INT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    related_product_id    INT NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    times_bought_together INT NOT NULL DEFAULT 0,
    PRIMARY KEY (product_id, related_product_id)
);

CREATE INDEX IF NOT EXISTS idx_orders_user        ON orders(user_id);
CREATE INDEX IF NOT EXISTS idx_notifications_user ON notifications(user_id);
CREATE INDEX IF NOT EXISTS idx_cart_user          ON cart_items(user_id);

-- ---------------------------------------------------------------- seed data
-- Prices are in Indian rupees. Product 2 has only 3 units left (shows the low-stock
-- state). Product 11 is out of stock (shows the disabled Add-to-cart state).

INSERT INTO products (id, name, description, price, category, image_emoji) VALUES
 (1,  'Wireless Headphones',        'Over-ear Bluetooth headphones with 30-hour battery life.', 2999.00, 'electronics', '🎧'),
 (2,  'Smart Watch',                'Fitness tracking, heart-rate sensor and AMOLED display.',  4999.00, 'electronics', '⌚'),
 (3,  'Portable Bluetooth Speaker', 'Waterproof speaker with deep bass and 12-hour playtime.',  1899.00, 'electronics', '🔊'),
 (4,  'Fast USB-C Charger',         '65 W GaN charger that powers laptops and phones.',          799.00, 'electronics', '🔌'),
 (5,  'Clean Code',                 'A handbook of agile software craftsmanship.',               599.00, 'books',       '📘'),
 (6,  'Designing Data-Intensive Applications', 'The big picture of reliable, scalable systems.', 1499.00, 'books',     '📗'),
 (7,  'The Go Programming Language','Practical introduction to Go and its concurrency model.',    899.00, 'books',       '📕'),
 (8,  'Cotton Hoodie',              'Soft brushed-cotton hoodie with a relaxed fit.',           1299.00, 'clothing',    '🧥'),
 (9,  'Running Shoes',              'Lightweight cushioned shoes for daily training.',          2499.00, 'clothing',    '👟'),
 (10, 'Ceramic Coffee Mug',         'Hand-glazed 350 ml mug, dishwasher safe.',                  349.00, 'home',        '☕'),
 (11, 'LED Desk Lamp',              'Dimmable lamp with three colour temperatures.',            1199.00, 'home',        '💡'),
 (12, 'Notebook Set',               'Three dotted notebooks with recycled paper.',               249.00, 'home',        '📓')
ON CONFLICT (id) DO NOTHING;

INSERT INTO inventory (product_id, stock_quantity) VALUES
 (1, 25), (2, 3), (3, 15), (4, 40), (5, 12), (6, 8),
 (7, 10), (8, 20), (9, 14), (10, 30), (11, 0), (12, 50)
ON CONFLICT (product_id) DO NOTHING;

-- Keep the SERIAL sequence ahead of the explicitly inserted ids.
SELECT setval(pg_get_serial_sequence('products', 'id'), (SELECT MAX(id) FROM products));
