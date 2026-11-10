#!/usr/bin/env python3
"""End-to-end smoke test for the main user journey, run through the API gateway.

Usage:
    BASE_URL=http://localhost:8080 python3 tests/smoke_test.py
"""
import sys
import uuid

from client import call

failures = 0


def check(label, condition, detail=""):
    global failures
    print(f"{'PASS' if condition else 'FAIL'}  {label}")
    if not condition:
        failures += 1
        if detail:
            print(f"      {detail}")


def main():
    email = f"smoke-{uuid.uuid4().hex[:8]}@example.com"
    password = "password123"

    status, body = call("POST", "/api/auth/register",
                        {"name": "Smoke Tester", "email": email, "password": password})
    check("register returns 201 with a token", status == 201 and "token" in body, f"{status} {body}")
    token = body.get("token", "")

    status, _ = call("GET", "/api/users/me")
    check("protected route rejects a request without a token (401)", status == 401, f"got {status}")

    status, body = call("GET", "/api/users/me", token=token)
    check("GET /api/users/me returns the caller's profile",
          status == 200 and body.get("email") == email, f"{status} {body}")

    status, _ = call("POST", "/api/auth/login", {"email": email, "password": "wrong-password"})
    check("login with a wrong password is rejected (401)", status == 401, f"got {status}")

    status, body = call("POST", "/api/auth/login", {"email": email, "password": password})
    check("login with the right password returns a token", status == 200 and "token" in body)

    status, dup = call("POST", "/api/auth/register",
                       {"name": "Dup", "email": email, "password": password})
    check("duplicate registration is rejected (409)", status == 409, f"got {status}")

    status, products = call("GET", "/api/products")
    check("catalogue lists products", status == 200 and len(products) > 0, f"{status} {products}")
    in_stock = next((p for p in products if p["stock"] >= 2), None)
    check("found a product with at least 2 units in stock", in_stock is not None)
    if in_stock is None:
        return 1

    status, cart = call("POST", "/api/cart", {"product_id": in_stock["id"], "quantity": 2}, token)
    check("add to cart returns 200 and two items", status == 200 and cart.get("item_count") == 2,
          f"{status} {cart}")

    status, _ = call("POST", "/api/cart", {"product_id": in_stock["id"], "quantity": 500}, token)
    check("quantity above the per-line limit is rejected (400)", status == 400, f"got {status}")

    status, _ = call("POST", "/api/cart", {"product_id": 999999, "quantity": 1}, token)
    check("adding an unknown product returns 404", status == 404, f"got {status}")

    status, placed = call("POST", "/api/checkout", token=token)
    order = placed.get("order") or {}
    check("checkout returns 201 with a CONFIRMED order",
          status == 201 and order.get("status") == "CONFIRMED", f"{status} {placed}")

    status, cart = call("GET", "/api/cart", token=token)
    check("cart is empty after checkout", status == 200 and cart.get("item_count") == 0)

    status, orders = call("GET", "/api/orders", token=token)
    check("the new order appears in order history",
          status == 200 and any(o["id"] == order.get("id") for o in orders), f"{status} {orders}")

    status, _ = call("POST", "/api/checkout", token=token)
    check("checkout with an empty cart is rejected (400)", status == 400, f"got {status}")

    status, recs = call("GET", "/api/recommendations", token=token)
    check("recommendations endpoint responds", status == 200 and "recommendations" in recs)

    status, _ = call("GET", "/api/products/999999")
    check("unknown product returns 404", status == 404, f"got {status}")

    status, _ = call("GET", "/api/orders/999999/invoice", token=token)
    check("invoice for an unknown order returns 404", status == 404, f"got {status}")

    print()
    if failures:
        print(f"{failures} check(s) FAILED")
        return 1
    print("All checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
