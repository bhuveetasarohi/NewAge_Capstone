"""Tiny HTTP helper shared by the test scripts. Standard library only."""
import json
import os
import urllib.error
import urllib.request

BASE_URL = os.getenv("BASE_URL", "http://localhost:8080").rstrip("/")


def call(method, path, body=None, token=None, timeout=20):
    """Return (status_code, parsed_body). Error responses are returned, not raised."""
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(BASE_URL + path, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as err:
        raw = err.read()
        try:
            return err.code, json.loads(raw)
        except ValueError:
            return err.code, raw


def register(prefix="tester"):
    """Create a fresh account and return its token."""
    import uuid

    email = f"{prefix}-{uuid.uuid4().hex[:10]}@example.com"
    status, body = call("POST", "/api/auth/register",
                        {"name": "Test User", "email": email, "password": "password123"})
    if status != 201:
        raise RuntimeError(f"register failed: {status} {body}")
    return body["token"]
