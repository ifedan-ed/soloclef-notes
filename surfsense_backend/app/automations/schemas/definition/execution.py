"""``Execution`` — automation-wide execution defaults (overridable per step)."""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, field_validator

from .plan_step import PlanStep

# Longest run time limit: 24 hours, the builder's maximum too. A larger value
# (sent through the API or written by the chat) is lowered to this rather than
# refused, so a definition saved before the cap still loads and runs; the
# overdue sweep's SQL caps stored snapshots the same way
# (``runtime/time_limit.py``).
MAX_TIMEOUT_SECONDS = 86_400


class Execution(BaseModel):
    model_config = ConfigDict(extra="forbid")

    timeout_seconds: int = Field(
        default=600,
        gt=0,
        json_schema_extra={"maximum": MAX_TIMEOUT_SECONDS},
        description=(
            "Wall-clock cap for the run, counted from when it starts. A run "
            "still going at the cap is stopped and recorded as timed_out. "
            f"At most {MAX_TIMEOUT_SECONDS} (24 hours); a larger value is "
            "lowered to that."
        ),
    )
    max_retries: int = Field(default=2, ge=0, description="Per-step retry budget.")
    retry_backoff: Literal["exponential", "linear", "none"] = "exponential"
    concurrency: Literal["drop_if_running", "queue", "always"] = Field(
        default="drop_if_running",
        description=(
            "When a trigger fires while a run is waiting or running: "
            "drop_if_running skips the fire, queue starts it after the runs "
            "ahead of it (one at a time, oldest first), always runs it at once "
            "alongside. See app/automations/concurrency."
        ),
    )
    on_failure: list[PlanStep] = Field(
        default_factory=list,
        description="Steps run when the main plan fails after retries.",
    )
    email_result: bool = Field(
        default=False,
        description=(
            "Email the automation's owner the final run output (a short "
            "email instead, on failure, needs_attention or timed_out; none "
            "for a cancelled run). Sent to the "
            "owner's account email only, at most once per run."
        ),
    )

    @field_validator("timeout_seconds")
    @classmethod
    def _cap_timeout(cls, value: int) -> int:
        """Lower a limit above ``MAX_TIMEOUT_SECONDS`` to it (see that constant)."""
        return min(value, MAX_TIMEOUT_SECONDS)
