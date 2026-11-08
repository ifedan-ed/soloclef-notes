"""The chat checkpointer's own pool, run against a real Postgres.

This is the thing that took the backend down when pgbouncer went in front of
Postgres: ``AsyncPostgresSaver.setup()`` died at startup with
``prepared statement "_pg3_0" does not exist``, because the pool asked psycopg
to prepare on first use (``prepare_threshold: 0``) and a prepared statement
does not survive to the next transaction under transaction pooling. Until now
the only evidence for the fix was a hand-run probe and a unit test that reads
the factory's source.

Point ``TEST_DATABASE_URL`` at a pooler and the first test is the production
path end to end. The second asserts the same invariant without needing one, so
a regression is caught on an ordinary direct run too: prepared statements must
be off, not merely eager, and a server connection must come back from the pool
carrying none.
"""

from __future__ import annotations

import uuid

import pytest
import pytest_asyncio
from langchain_core.messages import AIMessage, HumanMessage
from langgraph.checkpoint.base import empty_checkpoint

from app.agents.chat.runtime import checkpointer as checkpointer_module

pytestmark = pytest.mark.integration


@pytest_asyncio.fixture
async def checkpoint_saver():
    """The saver chat turns use, built and torn down by the production helpers.

    ``get_checkpointer()`` is what runs ``setup()``, so reaching the yield at
    all is already the startup crash not happening.
    """
    saver = await checkpointer_module.get_checkpointer()
    yield saver
    await checkpointer_module.close_checkpointer()


async def test_a_checkpoint_written_through_the_pool_reads_back(checkpoint_saver):
    checkpoint = empty_checkpoint()
    checkpoint["channel_values"] = {
        "messages": [HumanMessage(content="q"), AIMessage(content="a")],
    }
    checkpoint["channel_versions"] = {"messages": 1}
    thread = {
        "configurable": {
            "thread_id": f"pooled-{uuid.uuid4()}",
            "checkpoint_ns": "",
        }
    }

    saved = await checkpoint_saver.aput(
        thread, checkpoint, {"source": "loop", "step": 1}, {"messages": 1}
    )
    loaded = await checkpoint_saver.aget_tuple(saved)

    assert loaded is not None
    assert (
        loaded.checkpoint["channel_values"]["messages"]
        == checkpoint["channel_values"]["messages"]
    )


async def test_the_pool_hands_back_a_connection_with_no_prepared_statements(
    checkpoint_saver,
):
    """Statements and the readback share one transaction, so one backend answers.

    Without that the reply could come from a different server connection behind
    the pooler, which has had ``DISCARD ALL`` run on it and would look clean
    whatever the pool was configured to do.
    """
    async with checkpoint_saver.conn.connection() as conn, conn.transaction():
        for probe in range(3):
            await conn.execute(f"SELECT {probe} AS probe")
        named = await (
            await conn.execute("SELECT name FROM pg_prepared_statements")
        ).fetchall()

    assert [row["name"] for row in named] == []
