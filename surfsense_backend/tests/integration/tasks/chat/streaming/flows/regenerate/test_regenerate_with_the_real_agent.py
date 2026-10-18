"""Regenerate a turn that delegated to a sub-agent, with the production agent.

The assembled multi-agent graph (real middleware, real ``task`` tool, real
Postgres checkpointer) runs two turns; the second was asked with the Google
Search tool and delegated to the ``google_search`` sub-agent. Regenerate
finds the saved turn, rewinds, and re-runs it the way the route does. The
scripted model is shared by the main agent and the sub-agent and answers in
order.
"""

from __future__ import annotations

import pytest
from langchain.tools import ToolRuntime
from langchain_core.messages import AIMessage, HumanMessage, ToolMessage

from app.agents.chat.multi_agent_chat import create_multi_agent_chat_deep_agent
from app.agents.chat.multi_agent_chat.subagents.shared.invocation import (
    subagent_invoke_config,
)
from app.agents.chat.retrieval_scope import RetrievalScope
from app.agents.chat.tool_preference import ToolPreference
from app.services.connector_service import ConnectorService
from app.services.new_streaming_service import VercelStreamingService
from app.tasks.chat.streaming.agent.event_loop import stream_agent_events
from app.tasks.chat.streaming.flows.regenerate import (
    find_replaced_turn,
    find_rewind_point,
    turn_options_configurable,
)
from app.tasks.chat.streaming.shared.stream_result import StreamResult
from tests.integration.harness import ScriptedTurn, build_scripted_harness

pytestmark = pytest.mark.integration

_DELEGATE = ScriptedTurn(
    tool_calls=[
        {
            "name": "task",
            "args": {
                "subagent_type": "google_search",
                "description": "EVD output replacement",
            },
            # The same id on the re-run: the sub-agent must still start fresh.
            "id": "call_evd_search",
        }
    ]
)


def _texts(messages) -> list[str]:
    out = []
    for m in messages:
        if isinstance(m, AIMessage) and m.tool_calls:
            out.append("task(" + m.tool_calls[0]["args"]["subagent_type"] + ")")
        elif isinstance(m, HumanMessage | AIMessage | ToolMessage):
            out.append(str(m.content))
    return out


async def test_regenerate_replaces_a_delegated_turn_whole(
    db_session, db_user, db_workspace, chat, save_turn, pg_saver
):
    harness = build_scripted_harness(
        turns=[
            ScriptedTurn(text="An external ventricular drain."),
            _DELEGATE,
            ScriptedTurn(text="SERP: infection articles"),
            ScriptedTurn(text="Old answer about infections."),
            _DELEGATE,
            ScriptedTurn(text="SERP: Fleisher, Nelson on CSF losses"),
            ScriptedTurn(text="Replace ongoing losses mL for mL."),
        ]
    )
    agent = await create_multi_agent_chat_deep_agent(
        llm=harness.model,
        workspace_id=db_workspace.id,
        db_session=db_session,
        connector_service=ConnectorService(db_session),
        checkpointer=pg_saver,
        user_id=str(db_user.id),
        thread_id=chat.id,
        agent_config=None,
        retrieval_scope=RetrievalScope.ALL,
    )

    async def ask(question, turn_id, *, tool=None, from_checkpoint=None):
        configurable = {
            "thread_id": str(chat.id),
            "turn_id": turn_id,
            **turn_options_configurable(tool),
        }
        if from_checkpoint:
            configurable["checkpoint_id"] = from_checkpoint
        # Through the turn's own event loop, as ``stream_new_chat`` runs it.
        result = StreamResult()
        async for _frame in stream_agent_events(
            agent=agent,
            config={"configurable": configurable, "recursion_limit": 10_000},
            input_data={"messages": [HumanMessage(content=question)]},
            streaming_service=VercelStreamingService(),
            result=result,
        ):
            pass
        return result

    await ask("What is an EVD?", f"{chat.id}:1")
    await save_turn(
        chat,
        "What is an EVD?",
        "An external ventricular drain.",
        turn_id=f"{chat.id}:1",
    )
    await ask(
        "EVD replacing output?", f"{chat.id}:2", tool=ToolPreference.GOOGLE_SEARCH
    )
    await save_turn(
        chat,
        "EVD replacing output?",
        "Old answer about infections.",
        turn_id=f"{chat.id}:2",
    )

    replaced = await find_replaced_turn(
        db_session, thread_id=chat.id, from_message_id=None
    )
    point = await find_rewind_point(pg_saver, thread_id=chat.id, replaced=replaced)
    assert point.preferred_tool is ToolPreference.GOOGLE_SEARCH

    rerun = await ask(
        replaced.user_query,
        f"{chat.id}:3",
        tool=point.preferred_tool,
        from_checkpoint=point.checkpoint_id,
    )
    assert [p.get("text") for p in rerun.final_message_parts] == [
        "Replace ongoing losses mL for mL."
    ]

    state = await agent.aget_state({"configurable": {"thread_id": str(chat.id)}})
    texts = _texts(state.values["messages"])
    assert texts.count("EVD replacing output?") == 1
    assert "Old answer about infections." not in texts
    assert texts[-1] == "Replace ongoing losses mL for mL."
    assert texts.count("task(google_search)") == 1

    task_result = state.values["messages"][-2]
    assert isinstance(task_result, ToolMessage)
    assert "Fleisher" in str(task_result.content)

    # The sub-agent's own thread restarted: it saw one task, not both.
    sub_config = subagent_invoke_config(
        ToolRuntime(
            state=None,
            context=None,
            config={"configurable": {"thread_id": str(chat.id)}},
            stream_writer=None,
            tool_call_id="call_evd_search",
            store=None,
        )
    )
    # It checkpoints under the parent's task namespace; the newest is the re-run's.
    sub_latest = None
    async for saved in pg_saver.alist(
        {"configurable": {"thread_id": sub_config["configurable"]["thread_id"]}},
        limit=1,
    ):
        sub_latest = saved
    sub_messages = sub_latest.checkpoint["channel_values"]["messages"]
    assert [m.content for m in sub_messages if isinstance(m, HumanMessage)] == [
        "EVD output replacement"
    ]
