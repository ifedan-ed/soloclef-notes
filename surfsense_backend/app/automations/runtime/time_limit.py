"""A run's time limit, read from the definition it was launched with."""

from __future__ import annotations

from typing import Any

from sqlalchemy import Integer, Numeric, case, cast, func
from sqlalchemy.sql.elements import ColumnElement

from app.automations.schemas.definition.execution import (
    MAX_TIMEOUT_SECONDS,
    Execution,
)

DEFAULT_TIMEOUT_SECONDS: int = Execution.model_fields["timeout_seconds"].default


def snapshot_timeout_seconds(snapshot: Any) -> int:
    """``execution.timeout_seconds`` of a run's ``definition_snapshot``.

    Falls back to the schema default (600) for a snapshot that lacks it or
    holds something unusable, so a damaged row still gets a finite limit, and
    lowers a value saved before the 24-hour cap to ``MAX_TIMEOUT_SECONDS``,
    as loading the definition does. ``snapshot_timeout_sql`` is the same rule
    in SQL.
    """
    if isinstance(snapshot, dict):
        execution = snapshot.get("execution")
        if isinstance(execution, dict):
            value = execution.get("timeout_seconds")
            if isinstance(value, int) and not isinstance(value, bool) and value > 0:
                return min(value, MAX_TIMEOUT_SECONDS)
    return DEFAULT_TIMEOUT_SECONDS


def snapshot_timeout_sql(snapshot: Any) -> ColumnElement[int]:
    """:func:`snapshot_timeout_seconds` as SQL over a ``definition_snapshot`` column.

    Never raises on a stored row: anything but a positive whole JSON number
    gives the default, and a number above the cap gives the cap, so one odd
    row cannot break a query over every workspace's runs (as an INTEGER cast
    of a value past 2^31 once could).
    """
    raw = snapshot["execution"]["timeout_seconds"]
    number = cast(raw.astext, Numeric)
    # CASE tests its branches in order, so the cast to numeric only runs on
    # a JSON number.
    seconds = case(
        (func.jsonb_typeof(raw).is_distinct_from("number"), DEFAULT_TIMEOUT_SECONDS),
        ((number <= 0) | (number != func.trunc(number)), DEFAULT_TIMEOUT_SECONDS),
        else_=func.least(number, MAX_TIMEOUT_SECONDS),
    )
    return cast(seconds, Integer)


def run_timed_out_error(timeout_seconds: int) -> dict[str, Any]:
    """Run-level ``error`` when the executor stopped the run at its limit."""
    return {
        "type": "TimedOut",
        "message": (
            "Stopped after reaching its time limit of "
            f"{describe_seconds(timeout_seconds)}."
        ),
    }


def run_overdue_error(timeout_seconds: int) -> dict[str, Any]:
    """Run-level ``error`` when the overdue sweep found the run past its limit.

    The worker that ran it stopped without recording an outcome (restarted,
    killed, or wedged), so the run cannot have finished.
    """
    return {
        "type": "TimedOut",
        "message": (
            "Did not finish within its time limit of "
            f"{describe_seconds(timeout_seconds)}: the worker running it "
            "stopped before it could finish."
        ),
    }


def describe_seconds(seconds: int) -> str:
    """``600`` → ``"10 minutes"``; used in run errors and emails."""
    if seconds % 3600 == 0:
        hours = seconds // 3600
        return f"{hours} hour{'s' if hours != 1 else ''}"
    if seconds % 60 == 0:
        minutes = seconds // 60
        return f"{minutes} minute{'s' if minutes != 1 else ''}"
    return f"{seconds} second{'s' if seconds != 1 else ''}"
