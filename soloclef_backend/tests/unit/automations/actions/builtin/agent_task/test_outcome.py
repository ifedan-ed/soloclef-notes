"""An automation step is only a success when the agent did its task."""

from __future__ import annotations

import pytest
from langchain_core.messages import AIMessage, HumanMessage, ToolMessage

from app.automations.actions.builtin.agent_task.outcome import decide_outcome

pytestmark = pytest.mark.unit


def _turn(*messages):
    return {"messages": [HumanMessage(content="task"), *messages]}


def _report(status: str, reason: str) -> AIMessage:
    return AIMessage(
        content="",
        tool_calls=[
            {
                "name": "report_outcome",
                "args": {"status": status, "reason": reason},
                "id": "c1",
            }
        ],
    )


def test_a_reported_failure_is_a_failure_with_its_reason():
    result = _turn(
        _report("failed", "No notebook called Budget."),
        ToolMessage(content="ok", tool_call_id="c1", name="report_outcome"),
        AIMessage(content="I could not find it."),
    )
    outcome = decide_outcome(result, "I could not find it.")
    assert (outcome.status, outcome.reason) == ("failed", "No notebook called Budget.")


def test_a_reported_needs_attention_is_kept():
    outcome = decide_outcome(
        _turn(_report("needs_attention", "Needs the web.")), "Sorry."
    )
    assert (outcome.status, outcome.reason) == ("needs_attention", "Needs the web.")


def test_reported_done_with_an_answer_succeeds():
    outcome = decide_outcome(_turn(_report("done", "Listed.")), "A; B; C")
    assert (outcome.status, outcome.reason) == ("succeeded", "Listed.")


def test_reported_done_without_any_answer_is_a_failure():
    assert decide_outcome(_turn(_report("done", "ok")), None).status == "failed"


def test_no_answer_at_all_fails():
    outcome = decide_outcome(_turn(), None)
    assert outcome.status == "failed"
    assert "without an answer" in outcome.reason


@pytest.mark.parametrize(
    "answer",
    [
        "The scope excludes the web.\n\nWould you like me to do that and summarise the report?",
        "I need a decision. Shall I widen the scope",
        "Which notebook do you mean?",
    ],
)
def test_an_answer_that_hands_a_question_back_needs_attention(answer):
    outcome = decide_outcome(_turn(AIMessage(content=answer)), answer)
    assert outcome.status == "needs_attention"


def test_declined_approvals_need_attention():
    outcome = decide_outcome(_turn(), "Done what I could.", declined_actions=2)
    assert outcome.status == "needs_attention"
    assert "2 action(s)" in outcome.reason


def test_a_plain_answer_succeeds():
    answer = "Your notebooks: Grant ideas; Reading list."
    assert (
        decide_outcome(_turn(AIMessage(content=answer)), answer).status == "succeeded"
    )


def test_an_unknown_reported_status_is_ignored():
    result = _turn(_report("maybe", "?"))
    assert decide_outcome(result, "All done.").status == "succeeded"
