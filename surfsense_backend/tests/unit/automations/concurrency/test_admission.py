""" "If already running": whether a fire may create a run, given what is unfinished."""

from __future__ import annotations

import pytest

from app.automations.concurrency.admission import (
    QUEUE_LIMIT,
    Admission,
    decide_admission,
)

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("running", "waiting", "expected"),
    [
        (0, 0, Admission.CREATE),
        (1, 0, Admission.SKIP_ALREADY_RUNNING),
        (0, 1, Admission.SKIP_ALREADY_RUNNING),
    ],
)
def test_skip_if_already_running_counts_waiting_and_running_runs(
    running, waiting, expected
):
    """A run waiting to start counts as running: two fires in the same instant
    must not both get through."""
    assert (
        decide_admission("drop_if_running", running=running, waiting=waiting)
        is expected
    )


def test_queue_creates_while_another_run_is_going():
    assert decide_admission("queue", running=1, waiting=3) is Admission.CREATE


def test_queue_drops_fires_once_the_queue_is_full():
    assert (
        decide_admission("queue", running=1, waiting=QUEUE_LIMIT)
        is Admission.SKIP_QUEUE_FULL
    )


def test_always_run_creates_regardless():
    assert decide_admission("always", running=5, waiting=50) is Admission.CREATE
