"""AutomationRun state machine: pending → running → (succeeded|failed|needs_attention|cancelled|timed_out).

``needs_attention``: the run finished but did not do its task, and a person
has to act (widen what it may use, approve actions, connect something).
"""

from __future__ import annotations

from enum import StrEnum


class RunStatus(StrEnum):
    PENDING = "pending"
    RUNNING = "running"
    SUCCEEDED = "succeeded"
    FAILED = "failed"
    NEEDS_ATTENTION = "needs_attention"
    CANCELLED = "cancelled"
    TIMED_OUT = "timed_out"
