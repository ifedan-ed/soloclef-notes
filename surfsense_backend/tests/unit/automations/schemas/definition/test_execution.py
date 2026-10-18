"""Lock the ``Execution`` defaults + literal-constraint contract.

These defaults control production behavior of every automation that
doesn't override them; the defaults *are* the contract.
"""

from __future__ import annotations

import pytest
from pydantic import ValidationError

from app.automations.schemas.definition.execution import Execution

pytestmark = pytest.mark.unit


def test_execution_uses_production_defaults_when_no_overrides_provided() -> None:
    """The defaults shipped to prod: 10-minute wall clock, 2 retries
    per step, exponential backoff, drop overlapping runs. Changing any
    of these is a behavioral release-note change."""
    execution = Execution()

    assert execution.timeout_seconds == 600
    assert execution.max_retries == 2
    assert execution.retry_backoff == "exponential"
    assert execution.concurrency == "drop_if_running"
    assert execution.on_failure == []


def test_execution_rejects_unknown_retry_backoff_strategy() -> None:
    """``retry_backoff`` is constrained to a closed set — typos like
    ``"expontential"`` must fail validation, not silently coerce."""
    with pytest.raises(ValidationError):
        Execution(retry_backoff="expontential")  # type: ignore[arg-type]


def test_execution_rejects_unknown_concurrency_strategy() -> None:
    """Same closed-set constraint on ``concurrency``."""
    with pytest.raises(ValidationError):
        Execution(concurrency="parallel")  # type: ignore[arg-type]


def test_execution_rejects_invalid_numeric_bounds() -> None:
    """``timeout_seconds > 0`` and ``max_retries >= 0``. Zero or negative
    values would produce nonsensical run behavior."""
    with pytest.raises(ValidationError):
        Execution(timeout_seconds=0)
    with pytest.raises(ValidationError):
        Execution(max_retries=-1)


def test_execution_caps_the_run_time_limit_at_24_hours() -> None:
    """Above the builder's 24-hour maximum a limit is lowered, not refused,
    so a definition saved before the cap still loads (the automation page
    re-validates stored definitions)."""
    assert Execution(timeout_seconds=86_400).timeout_seconds == 86_400
    assert Execution(timeout_seconds=3_000_000_000).timeout_seconds == 86_400
    schema = Execution.model_json_schema()["properties"]["timeout_seconds"]
    assert schema["maximum"] == 86_400
