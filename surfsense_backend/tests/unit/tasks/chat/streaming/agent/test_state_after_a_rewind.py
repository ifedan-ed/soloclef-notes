"""After a re-run from a rewind point, the turn reads its own state, not the rewind point.

Regenerate starts the run from an earlier checkpoint
(``configurable.checkpoint_id``). The answer saved for the turn, its
citations and any approval it waits for come from the state read after the
run; read with that id, they were the previous turn's.
"""

from __future__ import annotations

from datetime import UTC, datetime
from typing import Annotated

import pytest
from langchain_core.messages import AIMessage, AnyMessage, HumanMessage
from langgraph.checkpoint.memory import InMemorySaver
from langgraph.graph import END, START, StateGraph
from langgraph.graph.message import add_messages
from typing_extensions import TypedDict

from app.services.new_streaming_service import VercelStreamingService
from app.tasks.chat.streaming.agent.event_loop import stream_agent_events
from app.tasks.chat.streaming.flows.regenerate import ReplacedTurn, find_rewind_point
from app.tasks.chat.streaming.shared.stream_result import StreamResult

pytestmark = pytest.mark.unit


class _State(TypedDict):
    messages: Annotated[list[AnyMessage], add_messages]


def _graph(saver: InMemorySaver, replies: list[str]):
    pending = list(replies)

    def model(_state: _State):
        return {"messages": [AIMessage(pending.pop(0))]}

    graph = StateGraph(_State)
    graph.add_node("model", model)
    graph.add_edge(START, "model")
    graph.add_edge("model", END)
    return graph.compile(checkpointer=saver)


async def test_the_rerun_reads_its_own_answer():
    saver = InMemorySaver()
    graph = _graph(saver, ["answer 1", "answer 2", "answer 3"])
    for question, turn_id in (("What is an EVD?", "117:1"), ("Output?", "117:2")):
        await graph.ainvoke(
            {"messages": [HumanMessage(question)]},
            {"configurable": {"thread_id": "117", "turn_id": turn_id}},
        )
    rewind_point = (
        await find_rewind_point(
            saver,
            thread_id=117,
            replaced=ReplacedTurn(
                user_message_id=2,
                turn_id="117:2",
                starts_at=datetime.now(UTC),
                user_query="Output?",
                image_urls=[],
                message_ids=[3, 4],
                turn_ids=["117:2"],
                is_latest_turn=True,
            ),
        )
    ).checkpoint_id

    result = StreamResult()
    config = {
        "configurable": {
            "thread_id": "117",
            "turn_id": "117:3",
            "checkpoint_id": rewind_point,
        }
    }
    async for _frame in stream_agent_events(
        agent=graph,
        config=config,
        input_data={"messages": [HumanMessage("Output?")]},
        streaming_service=VercelStreamingService(),
        result=result,
    ):
        pass

    assert [p.get("text") for p in result.final_message_parts] == ["answer 3"]
    state = await result.load_agent_state()
    assert [m.content for m in state.values["messages"]] == [
        "What is an EVD?",
        "answer 1",
        "Output?",
        "answer 3",
    ]
    # The rewind point itself is untouched.
    assert config["configurable"]["checkpoint_id"] == rewind_point
