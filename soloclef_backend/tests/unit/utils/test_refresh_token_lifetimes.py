"""Refresh-token families (app/utils/refresh_tokens.py, plan item B3): an app
token lives as long as its family (365 days, no idle timeout), a web token
slides; and the signed-in devices list groups rows into devices."""

from __future__ import annotations

import uuid
from datetime import UTC, datetime, timedelta
from types import SimpleNamespace

import pytest

from app.config import config
from app.utils.refresh_tokens import (
    KIND_APP,
    KIND_WEB,
    devices_from_rows,
    family_lifetime_seconds,
    token_expiry,
)

pytestmark = pytest.mark.unit

NOW = datetime(2026, 10, 10, 12, 0, tzinfo=UTC)
DAY = timedelta(days=1)


def test_an_app_family_lives_a_year_and_a_web_one_the_absolute_lifetime():
    assert family_lifetime_seconds(KIND_APP) == config.APP_SESSION_LIFETIME_SECONDS
    assert config.APP_SESSION_LIFETIME_SECONDS == 365 * 24 * 3600
    assert family_lifetime_seconds(KIND_WEB) == config.REFRESH_ABSOLUTE_LIFETIME_SECONDS
    assert (
        family_lifetime_seconds("anything else")
        == config.REFRESH_ABSOLUTE_LIFETIME_SECONDS
    )


def test_an_app_token_lives_as_long_as_its_family():
    cap = NOW + 365 * DAY
    assert token_expiry(KIND_APP, NOW, cap) == cap
    assert token_expiry(KIND_APP, NOW + 300 * DAY, cap) == cap


def test_a_web_token_slides_but_never_past_the_familys_cap():
    cap = NOW + 30 * DAY
    assert (
        token_expiry(KIND_WEB, NOW, cap, idle_seconds=14 * 24 * 3600) == NOW + 14 * DAY
    )
    assert (
        token_expiry(KIND_WEB, NOW + 20 * DAY, cap, idle_seconds=14 * 24 * 3600) == cap
    )
    assert token_expiry(KIND_WEB, NOW, cap) == min(
        NOW + timedelta(seconds=config.REFRESH_TOKEN_LIFETIME_SECONDS), cap
    )


def row(
    family,
    *,
    created,
    expires,
    revoked=None,
    cap=None,
    kind=KIND_APP,
    label="iPhone",
    signed_in=None,
):
    return SimpleNamespace(
        family_id=family,
        created_at=created,
        expires_at=expires,
        revoked_at=revoked,
        absolute_expiry=cap,
        kind=kind,
        device_label=label,
        signed_in_at=signed_in,
    )


def test_devices_are_families_with_a_live_token_dated_by_their_first_and_last_rows():
    phone, laptop, old = uuid.uuid4(), uuid.uuid4(), uuid.uuid4()
    cap = NOW + 300 * DAY
    rows = [
        # The phone signed in 10 days ago and refreshed twice; the newest token is live.
        row(phone, created=NOW - 10 * DAY, expires=cap, revoked=NOW - 5 * DAY, cap=cap),
        row(phone, created=NOW - 5 * DAY, expires=cap, revoked=NOW - 1 * DAY, cap=cap),
        row(
            phone,
            created=NOW - 1 * DAY,
            expires=cap,
            cap=cap,
            label="iPhone 15 Pro · Soloclef app",
        ),
        # A browser, used this morning.
        row(
            laptop,
            created=NOW - 2 * DAY,
            expires=NOW + 12 * DAY,
            cap=NOW + 28 * DAY,
            kind=KIND_WEB,
            label=None,
        ),
        # A device signed out: every row revoked.
        row(
            old,
            created=NOW - 40 * DAY,
            expires=NOW - 40 * DAY,
            revoked=NOW - 40 * DAY,
            cap=NOW - 10 * DAY,
        ),
    ]
    devices = devices_from_rows(rows, NOW)
    assert [d.family_id for d in devices] == [phone, laptop], (
        "newest use first; the signed-out one is gone"
    )
    assert devices[0].signed_in_at == NOW - 10 * DAY
    assert devices[0].last_used_at == NOW - 1 * DAY
    assert devices[0].device_label == "iPhone 15 Pro · Soloclef app"
    assert devices[0].kind == KIND_APP
    assert devices[0].expires_at == cap
    assert devices[1].kind == KIND_WEB and devices[1].device_label is None
    assert devices[1].expires_at == NOW + 12 * DAY


def test_an_expired_family_is_not_a_device():
    gone = uuid.uuid4()
    rows = [
        row(gone, created=NOW - 400 * DAY, expires=NOW - 35 * DAY, cap=NOW - 35 * DAY)
    ]
    assert devices_from_rows(rows, NOW) == []
    rows = [row(gone, created=NOW - 1 * DAY, expires=NOW + 1 * DAY, cap=NOW - 1 * DAY)]
    assert devices_from_rows(rows, NOW) == [], (
        "the family's cap ends it even with a token left"
    )


def test_the_sign_in_time_survives_the_purge_of_the_familys_older_rows():
    # purge_refresh_tokens deleted every rotated row: only the live one is
    # left, and it carries the family's sign-in time (c0027).
    phone = uuid.uuid4()
    cap = NOW + 300 * DAY
    rows = [
        row(
            phone,
            created=NOW - 1 * DAY,
            expires=cap,
            cap=cap,
            signed_in=NOW - 40 * DAY,
        )
    ]
    [device] = devices_from_rows(rows, NOW)
    assert device.signed_in_at == NOW - 40 * DAY
    assert device.last_used_at == NOW - 1 * DAY


def test_rows_from_before_the_sign_in_time_are_dated_by_their_earliest_row():
    legacy = uuid.uuid4()
    cap = NOW + 20 * DAY
    rows = [
        row(legacy, created=NOW - 9 * DAY, expires=cap, revoked=NOW - 2 * DAY, cap=cap),
        row(legacy, created=NOW - 2 * DAY, expires=cap, cap=cap),
    ]
    [device] = devices_from_rows(rows, NOW)
    assert device.signed_in_at == NOW - 9 * DAY
