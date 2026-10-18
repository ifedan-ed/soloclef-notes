"""Regenerate re-runs a turn from the agent's memory as it was before the question.

Drives a real LangGraph chat graph on an in-memory checkpointer, the way
``stream_new_chat`` runs a turn (``turn_id`` and the turn's options in
``configurable``, ``checkpoint_id`` for a re-run), with a scripted model that
records what it was shown. Its "task" tool runs a sub-agent graph on the same
checkpointer under the sub-agent's own thread id, as the real ``task`` tool
does.
"""

from __future__ import annotations

from datetime import UTC, datetime
from typing import Annotated, Any

import pytest
from langchain.tools import ToolRuntime
from langchain_core.messages import AIMessage, AnyMessage, HumanMessage, ToolMessage
from langchain_core.runnables import RunnableConfig
from langgraph.checkpoint.memory import InMemorySaver
from langgraph.graph import END, START, StateGraph
from langgraph.graph.message import add_messages
from typing_extensions import TypedDict

from app.agents.chat.multi_agent_chat.subagents.shared.invocation import (
    subagent_invoke_config,
)
from app.agents.chat.tool_preference import ToolPreference
from app.tasks.chat.streaming.flows.regenerate import (
    ReplacedTurn,
    find_rewind_point,
    turn_options_configurable,
)

pytestmark = pytest.mark.unit

THREAD = 117


class _State(TypedDict):
    messages: Annotated[list[AnyMessage], add_messages]


class _ScriptedModel:
    """Answers with the next scripted message and records what it was shown."""

    def __init__(self, replies: list[AIMessage]) -> None:
        self.replies = list(replies)
        self.shown: list[list[str]] = []

    def __call__(self, state: _State) -> dict[str, Any]:
        self.shown.append([_text(m) for m in state["messages"]])
        return {"messages": [self.replies.pop(0)]}


def _text(message: AnyMessage) -> str:
    if isinstance(message, AIMessage) and message.tool_calls:
        return "task(" + ", ".join(c["id"] for c in message.tool_calls) + ")"
    return str(message.content)


def _sub_agent(saver: InMemorySaver, model: _ScriptedModel):
    graph = StateGraph(_State)
    graph.add_node("model", model)
    graph.add_edge(START, "model")
    graph.add_edge("model", END)
    return graph.compile(checkpointer=saver)


def _chat_graph(saver: InMemorySaver, model: _ScriptedModel, sub_model: _ScriptedModel):
    sub_agent = _sub_agent(saver, sub_model)

    async def tools(state: _State, config: RunnableConfig) -> dict[str, Any]:
        call = state["messages"][-1].tool_calls[0]
        runtime = ToolRuntime(
            state=None,
            context=None,
            config=config,
            stream_writer=None,
            tool_call_id=call["id"],
            store=None,
        )
        result = await sub_agent.ainvoke(
            {"messages": [HumanMessage(content=call["args"]["description"])]},
            subagent_invoke_config(runtime),
        )
        return {
            "messages": [
                ToolMessage(
                    content=result["messages"][-1].content, tool_call_id=call["id"]
                )
            ]
        }

    def route(state: _State) -> str:
        last = state["messages"][-1]
        return "tools" if isinstance(last, AIMessage) and last.tool_calls else END

    graph = StateGraph(_State)
    graph.add_node("model", model)
    graph.add_node("tools", tools)
    graph.add_edge(START, "model")
    graph.add_conditional_edges("model", route, ["tools", END])
    graph.add_edge("tools", "model")
    return graph.compile(checkpointer=saver)


def _task_call(call_id: str) -> AIMessage:
    return AIMessage(
        content="",
        tool_calls=[
            {
                "name": "task",
                "args": {"description": "look up EVD output replacement"},
                "id": call_id,
            }
        ],
    )


async def _ask(
    graph,
    question: str,
    *,
    turn_id: str | None,
    from_checkpoint: str | None = None,
    tool: ToolPreference | None = None,
    history: list[AnyMessage] | None = None,
) -> None:
    """One turn, configured the way ``stream_new_chat`` configures it.

    ``history`` is what ``needs_history_bootstrap`` puts before the question:
    a copied chat's saved messages.
    """
    configurable: dict[str, Any] = {
        "thread_id": str(THREAD),
        **turn_options_configurable(tool),
    }
    if turn_id is not None:
        configurable["turn_id"] = turn_id
    if from_checkpoint is not None:
        configurable["checkpoint_id"] = from_checkpoint
    await graph.ainvoke(
        {"messages": [*(history or []), HumanMessage(content=question)]},
        {"configurable": configurable},
    )


async def _memory(graph) -> list[str]:
    state = await graph.aget_state({"configurable": {"thread_id": str(THREAD)}})
    return [_text(m) for m in state.values["messages"]]


def _replacing(*turn_ids: str | None, latest: bool = True) -> ReplacedTurn:
    """The saved rows of a replaced turn, as ``find_replaced_turn`` reports them."""
    return ReplacedTurn(
        user_message_id=1,
        turn_id=turn_ids[0],
        starts_at=datetime.now(UTC),
        user_query="",
        image_urls=[],
        message_ids=[1, 2],
        turn_ids=[t for t in turn_ids if t],
        is_latest_turn=latest,
    )


Q1, Q2 = "What is an EVD?", "EVD replacing output?"


@pytest.fixture
def saver() -> InMemorySaver:
    return InMemorySaver()


async def test_rerun_after_a_finished_turn_does_not_see_the_replaced_answer(saver):
    model = _ScriptedModel(
        [AIMessage("answer 1"), AIMessage("answer 2"), AIMessage("answer 3")]
    )
    graph = _chat_graph(saver, model, _ScriptedModel([]))
    await _ask(graph, Q1, turn_id="117:1")
    await _ask(graph, Q2, turn_id="117:2")

    point = await find_rewind_point(
        saver, thread_id=THREAD, replaced=_replacing("117:2")
    )
    await _ask(graph, Q2, turn_id="117:3", from_checkpoint=point.checkpoint_id)

    assert model.shown[-1] == [Q1, "answer 1", Q2]
    assert await _memory(graph) == [Q1, "answer 1", Q2, "answer 3"]
    assert point.rebuild_history is False


async def test_the_tool_the_question_was_asked_with_comes_back(saver):
    model = _ScriptedModel(
        [AIMessage("answer 1"), AIMessage("answer 2"), AIMessage("answer 3")]
    )
    graph = _chat_graph(saver, model, _ScriptedModel([]))
    await _ask(graph, Q1, turn_id="117:1")
    await _ask(graph, Q2, turn_id="117:2", tool=ToolPreference.GOOGLE_SEARCH)

    point = await find_rewind_point(
        saver, thread_id=THREAD, replaced=_replacing("117:2")
    )
    assert point.preferred_tool is ToolPreference.GOOGLE_SEARCH

    # The re-run records it again, so the next Regenerate still has it.
    await _ask(
        graph,
        Q2,
        turn_id="117:3",
        from_checkpoint=point.checkpoint_id,
        tool=point.preferred_tool,
    )
    again = await find_rewind_point(
        saver, thread_id=THREAD, replaced=_replacing("117:3")
    )
    assert again.preferred_tool is ToolPreference.GOOGLE_SEARCH

    first = await find_rewind_point(
        saver, thread_id=THREAD, replaced=_replacing("117:1")
    )
    assert first.preferred_tool is None


async def test_regenerating_twice_still_starts_before_the_question(saver):
    model = _ScriptedModel([AIMessage(f"answer {n}") for n in range(1, 5)])
    graph = _chat_graph(saver, model, _ScriptedModel([]))
    await _ask(graph, Q1, turn_id="117:1")
    await _ask(graph, Q2, turn_id="117:2")

    first = await find_rewind_point(
        saver, thread_id=THREAD, replaced=_replacing("117:2")
    )
    await _ask(graph, Q2, turn_id="117:3", from_checkpoint=first.checkpoint_id)
    # The first Regenerate's abandoned branch (answer 2) is older than turn
    # 117:3 but is not its parent.
    second = await find_rewind_point(
        saver, thread_id=THREAD, replaced=_replacing("117:3")
    )
    await _ask(graph, Q2, turn_id="117:4", from_checkpoint=second.checkpoint_id)

    assert second.checkpoint_id == first.checkpoint_id
    assert second.rebuild_history is False
    assert model.shown[-1] == [Q1, "answer 1", Q2]
    assert await _memory(graph) == [Q1, "answer 1", Q2, "answer 4"]


async def test_a_turn_that_used_a_sub_agent_is_replaced_whole(saver):
    model = _ScriptedModel(
        [
            AIMessage("answer 1"),
            _task_call("call_1"),
            AIMessage("answer 2"),
            # A provider can hand out the same call id again; the sub-agent
            # must still start fresh.
            _task_call("call_1"),
            AIMessage("answer 3"),
        ]
    )
    sub_model = _ScriptedModel([AIMessage("found 1"), AIMessage("found 2")])
    graph = _chat_graph(saver, model, sub_model)
    await _ask(graph, Q1, turn_id="117:1")
    await _ask(graph, Q2, turn_id="117:2", tool=ToolPreference.WEB)

    point = await find_rewind_point(
        saver, thread_id=THREAD, replaced=_replacing("117:2")
    )
    await _ask(
        graph,
        Q2,
        turn_id="117:3",
        from_checkpoint=point.checkpoint_id,
        tool=point.preferred_tool,
    )

    assert point.preferred_tool is ToolPreference.WEB
    assert await _memory(graph) == [
        Q1,
        "answer 1",
        Q2,
        "task(call_1)",
        "found 2",
        "answer 3",
    ]
    assert sub_model.shown[-1] == ["look up EVD output replacement"]


async def test_the_first_turn_of_a_chat_reruns_from_an_empty_memory(saver):
    model = _ScriptedModel([AIMessage("answer 1"), AIMessage("answer 2")])
    graph = _chat_graph(saver, model, _ScriptedModel([]))
    await _ask(graph, Q1, turn_id="117:1")

    point = await find_rewind_point(
        saver, thread_id=THREAD, replaced=_replacing("117:1")
    )
    await _ask(graph, Q1, turn_id="117:2", from_checkpoint=point.checkpoint_id)

    # Saved messages older than the question (a copied chat's) are given
    # again as history; this chat has none.
    assert point.rebuild_history is True
    assert model.shown[-1] == [Q1]
    assert await _memory(graph) == [Q1, "answer 2"]


def _copied_history() -> list[AnyMessage]:
    """A copied chat's saved messages, as ``bootstrap_history_from_db`` gives them."""
    return [HumanMessage("copied question"), AIMessage("copied answer")]


async def _rerun(graph, saver, question: str, *, replaced: ReplacedTurn, turn_id: str):
    """A Regenerate or Edit, the way the route runs it: the copied history is
    given again only when the rewind point says so."""
    point = await find_rewind_point(saver, thread_id=THREAD, replaced=replaced)
    await _ask(
        graph,
        question,
        turn_id=turn_id,
        from_checkpoint=point.checkpoint_id,
        history=_copied_history() if point.rebuild_history else None,
    )
    return point


async def test_a_copied_chats_first_question_keeps_its_history_regenerated_twice(
    saver,
):
    model = _ScriptedModel([AIMessage(f"answer {n}") for n in range(1, 4)])
    graph = _chat_graph(saver, model, _ScriptedModel([]))
    await _ask(graph, Q1, turn_id="117:1", history=_copied_history())

    first = await _rerun(
        graph, saver, Q1, replaced=_replacing("117:1"), turn_id="117:2"
    )
    # The second Regenerate's turn has a parent: the chat's empty start,
    # which the first re-run forked from.
    second = await _rerun(
        graph, saver, Q1, replaced=_replacing("117:2"), turn_id="117:3"
    )

    assert second.checkpoint_id == first.checkpoint_id
    assert first.rebuild_history is True
    assert second.rebuild_history is True
    assert model.shown == [["copied question", "copied answer", Q1]] * 3
    assert await _memory(graph) == ["copied question", "copied answer", Q1, "answer 3"]


async def test_an_edit_after_regenerating_a_copied_chats_first_question_keeps_its_history(
    saver,
):
    model = _ScriptedModel([AIMessage(f"answer {n}") for n in range(1, 5)])
    graph = _chat_graph(saver, model, _ScriptedModel([]))
    await _ask(graph, Q1, turn_id="117:1", history=_copied_history())
    await _rerun(graph, saver, Q1, replaced=_replacing("117:1"), turn_id="117:2")
    await _ask(graph, Q2, turn_id="117:3")

    # Edit the first question: it and the later turn are replaced.
    point = await _rerun(
        graph,
        saver,
        "What is an EVD for?",
        replaced=_replacing("117:2", "117:3", latest=False),
        turn_id="117:4",
    )

    assert point.rebuild_history is True
    assert model.shown[-1] == [
        "copied question",
        "copied answer",
        "What is an EVD for?",
    ]
    assert await _memory(graph) == [
        "copied question",
        "copied answer",
        "What is an EVD for?",
        "answer 4",
    ]


async def test_an_edit_of_an_earlier_question_drops_the_later_turns(saver):
    model = _ScriptedModel([AIMessage(f"answer {n}") for n in range(1, 5)])
    graph = _chat_graph(saver, model, _ScriptedModel([]))
    await _ask(graph, Q1, turn_id="117:1")
    await _ask(graph, Q2, turn_id="117:2")
    await _ask(graph, "and in neonates?", turn_id="117:3")

    point = await find_rewind_point(
        saver, thread_id=THREAD, replaced=_replacing("117:2", "117:3", latest=False)
    )
    await _ask(
        graph, "EVD output?", turn_id="117:4", from_checkpoint=point.checkpoint_id
    )

    assert await _memory(graph) == [Q1, "answer 1", "EVD output?", "answer 4"]


async def test_a_question_saved_before_turn_ids_is_found_by_its_message(saver):
    model = _ScriptedModel(
        [AIMessage("answer 1"), AIMessage("answer 2"), AIMessage("answer 3")]
    )
    graph = _chat_graph(saver, model, _ScriptedModel([]))
    await _ask(graph, Q1, turn_id=None)
    await _ask(graph, Q2, turn_id=None)

    point = await find_rewind_point(saver, thread_id=THREAD, replaced=_replacing(None))
    await _ask(graph, Q2, turn_id="117:3", from_checkpoint=point.checkpoint_id)

    assert point.rebuild_history is False
    assert await _memory(graph) == [Q1, "answer 1", Q2, "answer 3"]


async def test_a_turn_that_never_reached_the_agent_reruns_on_the_latest_state(saver):
    model = _ScriptedModel([AIMessage("answer 1")])
    graph = _chat_graph(saver, model, _ScriptedModel([]))
    await _ask(graph, Q1, turn_id="117:1")
    latest = await saver.aget_tuple({"configurable": {"thread_id": str(THREAD)}})

    point = await find_rewind_point(
        saver, thread_id=THREAD, replaced=_replacing("117:9")
    )

    assert point.checkpoint_id is None
    assert point.latest_checkpoint_id == latest.config["configurable"]["checkpoint_id"]


async def test_a_chat_with_no_memory_yet_reruns_fresh(saver):
    point = await find_rewind_point(
        saver, thread_id=THREAD, replaced=_replacing("117:1")
    )

    assert point.checkpoint_id is None
    assert point.latest_checkpoint_id is None
