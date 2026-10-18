"""Reading a run's time limit from its snapshot, and saying it in words."""

from __future__ import annotations

import pytest

from app.automations.runtime.time_limit import (
    DEFAULT_TIMEOUT_SECONDS,
    describe_seconds,
    run_timed_out_error,
    snapshot_timeout_seconds,
)

pytestmark = pytest.mark.unit


def test_reads_the_snapshot_limit():
    assert snapshot_timeout_seconds({"execution": {"timeout_seconds": 90}}) == 90


def test_a_stored_limit_above_the_cap_reads_as_24_hours():
    snapshot = {"execution": {"timeout_seconds": 3_000_000_000}}
    assert snapshot_timeout_seconds(snapshot) == 86_400


@pytest.mark.parametrize(
    "snapshot",
    [
        None,
        {},
        {"execution": None},
        {"execution": {"timeout_seconds": "x"}},
        {"execution": {"timeout_seconds": 0}},
    ],
)
def test_falls_back_to_the_default_for_a_damaged_snapshot(snapshot):
    assert snapshot_timeout_seconds(snapshot) == DEFAULT_TIMEOUT_SECONDS == 600


@pytest.mark.parametrize(
    ("seconds", "words"),
    [
        (600, "10 minutes"),
        (60, "1 minute"),
        (3600, "1 hour"),
        (7200, "2 hours"),
        (90, "90 seconds"),
        (1, "1 second"),
    ],
)
def test_describe_seconds(seconds, words):
    assert describe_seconds(seconds) == words


def test_the_timed_out_error_names_the_limit():
    error = run_timed_out_error(600)
    assert error["type"] == "TimedOut"
    assert "10 minutes" in error["message"]
