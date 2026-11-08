"""``GET /health``: the liveness probe container healthchecks hit."""

from __future__ import annotations

import pytest
from starlette.testclient import TestClient

from app.app import app, health_check, limiter

pytestmark = pytest.mark.unit


def test_health_needs_no_credentials():
    """Probes carry no token."""
    response = TestClient(app).get("/health")

    assert response.status_code == 200
    assert response.json() == {"status": "ok"}


def test_health_stays_exempt_from_rate_limiting():
    """A limited /health would 429 a probe and mark a healthy service down.

    Asserted against the limiter's registry rather than by exhausting a bucket:
    the registry is exact and instant, where hammering is slow and depends on
    whatever the configured limit happens to be.
    """
    assert f"{health_check.__module__}.{health_check.__name__}" in (
        limiter._exempt_routes
    )
