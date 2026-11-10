#!/usr/bin/env python3
"""Concurrency checks for the checkout path (Phase I objective: correctness under concurrent orders).

Scenario 1, correctness: 50 shoppers each put 1 unit in their cart for a product with only
20 units, then all press "Place order" at the same moment. Expected: exactly 20 orders
succeed, 30 are rejected with 409, and final stock is exactly 0 (never negative).

Scenario 2, throughput: 50 shoppers each place 4 orders against plenty of stock. Prints
orders per second for the checkout calls.

Both scenarios create products in the "load-test" category.

Usage:
    BASE_URL=http://localhost:8080 python3 tests/concurrency_check.py
"""
import collections
import concurrent.futures as cf
import sys
import threading
import time

from client import call, register

SHOPPERS = 50


def create_product(token, stock, name):
    status, body = call("POST", "/api/products", {
        "name": name,
        "description": "Created by the concurrency test.",
        "price": 100,
        "category": "load-test",
        "emoji": "🧪",
        "stock": stock,
    }, token)
    if status != 201:
        raise RuntimeError(f"could not create product: {status} {body}")
    return body["id"]


def stock_of(product_id):
    _, level = call("GET", f"/api/inventory/{product_id}")
    return level["stock_quantity"]


def run_parallel(fn, items):
    with cf.ThreadPoolExecutor(max_workers=SHOPPERS) as pool:
        return list(pool.map(fn, items))


def scenario_oversell_guard(failures):
    print("Scenario 1: 50 shoppers race for 20 units")
    tokens = run_parallel(lambda _: register("race"), range(SHOPPERS))
    product_id = create_product(tokens[0], stock=20, name="Limited Edition Item")

    for token in tokens:
        status, _ = call("POST", "/api/cart", {"product_id": product_id, "quantity": 1}, token)
        if status != 200:
            raise RuntimeError(f"add to cart failed: {status}")

    barrier = threading.Barrier(SHOPPERS)

    def race(token):
        barrier.wait()  # release every checkout at the same instant
        status, _ = call("POST", "/api/checkout", token=token)
        return status

    results = collections.Counter(run_parallel(race, tokens))
    remaining = stock_of(product_id)
    print(f"  results: {dict(results)}   final stock: {remaining}")

    checks = [
        ("exactly 20 orders succeeded", results[201] == 20),
        ("exactly 30 orders were rejected with 409", results[409] == 30),
        ("stock is exactly 0 (never negative)", remaining == 0),
    ]
    for label, ok in checks:
        print(f"  {'PASS' if ok else 'FAIL'}  {label}")
        if not ok:
            failures.append(label)


def scenario_throughput(failures):
    print("\nScenario 2: 50 shoppers x 4 orders each, plenty of stock")
    tokens = run_parallel(lambda _: register("load"), range(SHOPPERS))
    product_id = create_product(tokens[0], stock=1000, name="High Volume Item")

    def shopper(token):
        outcomes = []
        for _ in range(4):
            call("POST", "/api/cart", {"product_id": product_id, "quantity": 1}, token)
            status, _ = call("POST", "/api/checkout", token=token)
            outcomes.append(status)
        return outcomes

    started = time.perf_counter()
    outcomes = [s for batch in run_parallel(shopper, tokens) for s in batch]
    elapsed = time.perf_counter() - started

    ok = sum(1 for s in outcomes if s == 201)
    rate = ok / elapsed if elapsed else 0.0
    print(f"  {ok}/{len(outcomes)} orders succeeded in {elapsed:.2f}s  ->  {rate:.1f} orders/sec")
    print(f"  final stock: {stock_of(product_id)} (expected {1000 - ok})")

    label = "every order succeeded under load"
    print(f"  {'PASS' if ok == len(outcomes) else 'FAIL'}  {label}")
    if ok != len(outcomes):
        failures.append(label)


def main():
    failures = []
    scenario_oversell_guard(failures)
    scenario_throughput(failures)
    print()
    if failures:
        print(f"{len(failures)} check(s) FAILED: {failures}")
        return 1
    print("All concurrency checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main())
