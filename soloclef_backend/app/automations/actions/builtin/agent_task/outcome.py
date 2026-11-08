"""Decide whether an automation's agent actually did its task.

An agent turn that ends without an exception is not a success by itself: in
the live test one run listed Studio files instead of notebooks, and another
said it could not reach the web and asked for permission, and both were
marked "succeeded". Nobody is there to answer an unattended run, so:

* the agent reports its outcome with the ``report_outcome`` tool
  (``done`` / ``failed`` / ``needs_attention`` and a reason), which the run
  honours;
* when it did not report, a turn that ended with no answer is ``failed``, and
  one whose answer ends by asking a question, or that had to decline actions
  needing approval, is ``needs_attention``;
* otherwise it is ``succeeded``.

``report_outcome`` is stateless: its call and arguments are read back from the
turn's messages, so a compiled graph cached across runs cannot carry one run's
outcome into another.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Annotated, Any, Literal

from langchain_core.messages import AIMessage
from langchain_core.tools import BaseTool, StructuredTool

REPORT_OUTCOME = "report_outcome"

SUCCEEDED = "succeeded"
FAILED = "failed"
NEEDS_ATTENTION = "needs_attention"

_REPORTED = {"done": SUCCEEDED, "failed": FAILED, "needs_attention": NEEDS_ATTENTION}
MAX_REASON_CHARS = 500

# The answer ends by handing a decision back to a person who is not there.
_ASKS_BACK = re.compile(
    r"(\?\s*\**\s*$)|\b(would you like me to|shall i|should i|do you want me to|"
    r"let me know if you(?:'d| would) like)\b",
    re.IGNORECASE,
)


@dataclass(frozen=True)
class Outcome:
    status: str
    reason: str | None = None

    def as_dict(self) -> dict[str, Any]:
        return {"status": self.status, "reason": self.reason}


def create_report_outcome_tool() -> BaseTool:
    async def _report_outcome(
        status: Annotated[
            Literal["done", "failed", "needs_attention"],
            "done: the task was completed. failed: it could not be done. "
            "needs_attention: a person has to act (connect something, widen "
            "what the automation may use, approve an action, answer a question).",
        ],
        reason: Annotated[
            str,
            "One or two sentences for the automation's owner: what was done, "
            "or what went wrong and what they can change.",
        ],
    ) -> str:
        del status, reason
        return "Outcome recorded. Now give your final answer."

    return StructuredTool.from_function(
        name=REPORT_OUTCOME,
        description=(
            "Report whether this unattended automation run did its task. Call "
            "it exactly once, just before your final answer. Use 'failed' or "
            "'needs_attention' whenever you could not do what was asked (a "
            "tool was unavailable or refused, the data was not there, you "
            "need a decision): never present an unfinished task as done."
        ),
        coroutine=_report_outcome,
    )


def _reported(messages: list[Any]) -> Outcome | None:
    for message in reversed(messages):
        if not isinstance(message, AIMessage):
            continue
        for call in reversed(message.tool_calls or []):
            if call.get("name") != REPORT_OUTCOME:
                continue
            args = call.get("args") or {}
            status = _REPORTED.get(str(args.get("status") or "").strip().lower())
            if status is None:
                continue
            reason = str(args.get("reason") or "").strip()[:MAX_REASON_CHARS]
            return Outcome(status, reason or None)
    return None


def _last_line(text: str) -> str:
    lines = [line.strip() for line in text.strip().splitlines() if line.strip()]
    return lines[-1] if lines else ""


def decide_outcome(
    result: Any,
    final_message: str | None,
    *,
    declined_actions: int = 0,
) -> Outcome:
    """The run's honest outcome for one agent turn."""
    messages = result.get("messages") if isinstance(result, dict) else None
    reported = _reported(messages) if isinstance(messages, list) else None
    if reported is not None:
        if reported.status == SUCCEEDED and not final_message:
            return Outcome(FAILED, reported.reason or "The agent gave no answer.")
        return reported
    if not final_message:
        return Outcome(FAILED, "The agent finished without an answer.")
    if declined_actions:
        return Outcome(
            NEEDS_ATTENTION,
            f"{declined_actions} action(s) needed approval and were declined "
            "because this step does not auto-approve agent actions.",
        )
    if _ASKS_BACK.search(_last_line(final_message)):
        return Outcome(
            NEEDS_ATTENTION,
            "The agent stopped to ask a question instead of finishing: "
            + _last_line(final_message)[:300],
        )
    return Outcome(SUCCEEDED)
