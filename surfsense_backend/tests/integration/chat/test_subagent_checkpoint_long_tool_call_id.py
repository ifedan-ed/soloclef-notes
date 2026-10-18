"""A Gemini-sized tool_call_id must not blow up LangGraph's checkpoint tables.

``subagent_invoke_config`` namespaces a subagent's ``thread_id`` as
``{parent}::task:{tool_call_id}``. Gemini's ``tool_call.id`` carries a thought
signature and routinely runs 500-3,000+ chars; embedded verbatim, that pushed
``checkpoint_blobs``'/``checkpoints``' composite primary key
(``thread_id``, ``checkpoint_ns``, ``channel``, ``version``) past Postgres's
~2.7 kB btree entry cap, aborting the turn with
``ProgramLimitExceeded: index row size ... exceeds btree version 4 maximum
2704 for index "checkpoint_blobs_pkey"`` (observed 2026-10-01, thread 117,
during an ``email_imap`` subagent call).

We don't own LangGraph's schema, so the fix lives entirely on our side: the
``task:`` suffix is a short sha256-derived key instead of the raw id (see
``app/agents/chat/multi_agent_chat/subagents/shared/invocation.py``). This
test drives the real ``get_checkpointer()`` Postgres saver — the one actual
subagent runs use — with a 3,000+ char tool_call_id, the same way
``test_checkpoint_serde_postgres.py`` exercises the main thread's checkpoint
path.
"""

from __future__ import annotations

import secrets
import uuid

import pytest
from langchain.tools import ToolRuntime
from langchain_core.messages import HumanMessage
from langgraph.checkpoint.base import empty_checkpoint

from app.agents.chat.multi_agent_chat.subagents.shared.invocation import (
    subagent_invoke_config,
)
from app.agents.chat.runtime import checkpointer as checkpointer_module

pytestmark = pytest.mark.integration


def _gemini_call_id(length: int) -> str:
    prefix = "call_1__thought__"
    return prefix + secrets.token_hex(length)[: length - len(prefix)]


async def test_subagent_checkpoint_survives_a_3000_char_tool_call_id():
    long_call_id = _gemini_call_id(3_000)
    assert len(long_call_id) == 3_000

    parent_thread_id = f"chat-{uuid.uuid4()}"
    runtime = ToolRuntime(
        state=None,
        context=None,
        config={"configurable": {"thread_id": parent_thread_id}},
        stream_writer=None,
        tool_call_id=long_call_id,
        store=None,
    )
    sub_config = subagent_invoke_config(runtime)
    thread_id = sub_config["configurable"]["thread_id"]

    # The whole point of the fix: the raw id never reaches the checkpoint key,
    # and the composite pkey (thread_id, checkpoint_ns, channel, version)
    # stays far under Postgres's ~2.7kB btree entry cap.
    assert long_call_id not in thread_id
    assert len(thread_id) < 200

    saver = await checkpointer_module.get_checkpointer()
    try:
        checkpoint = empty_checkpoint()
        checkpoint["channel_values"] = {
            "messages": [HumanMessage(content="search my email for insurance quotes")]
        }
        checkpoint["channel_versions"] = {"messages": 1}
        config = {"configurable": {"thread_id": thread_id, "checkpoint_ns": ""}}

        # This is exactly the call that raised ProgramLimitExceeded in
        # production when the raw tool_call_id was embedded in thread_id.
        saved = await saver.aput(
            config,
            checkpoint,
            {"source": "loop", "step": 1},
            {"messages": 1},
        )
        await saver.aput_writes(
            saved,
            [("messages", [HumanMessage(content="search my email")])],
            "task-1",
        )
        loaded = await saver.aget_tuple(saved)

        assert loaded is not None
        assert loaded.config["configurable"]["thread_id"] == thread_id

        await saver.adelete_thread(thread_id)
    finally:
        await checkpointer_module.close_checkpointer()
