"""A chat started outside a notebook is filed in the workspace's "Earlier chats".

The web app opens chats only inside notebooks, so a chat created elsewhere (a
copied public chat) is filed where it can open.
"""

from __future__ import annotations

import secrets

import pytest
from fastapi import Response
from sqlalchemy import select

from app.db import (
    ARTIFACT_API_SOURCE,
    ChatVisibility,
    NewChatMessage,
    NewChatMessageRole,
    NewChatThread,
    PublicChatSnapshot,
)
from app.notebooks.earlier_chats import (
    EARLIER_CHATS_TITLE,
    NotebookEarlierChat,
    place_thread,
)
from app.notebooks.persistence import Notebook
from app.notebooks.routes import threads
from app.services.public_chat_service import clone_from_snapshot

pytestmark = pytest.mark.integration


async def _standalone(db_session, owner, **overrides) -> NewChatThread:
    thread = NewChatThread(
        title=overrides.pop("title", "Old chat"),
        workspace_id=owner.workspace.id,
        created_by_id=overrides.pop("created_by_id", owner.user.id),
        visibility=overrides.pop("visibility", ChatVisibility.PRIVATE),
        **overrides,
    )
    db_session.add(thread)
    await db_session.flush()
    db_session.add(
        NewChatMessage(
            thread_id=thread.id,
            role=NewChatMessageRole.USER,
            content=[{"type": "text", "text": "hello"}],
        )
    )
    await db_session.flush()
    return thread


async def test_standalone_chats_share_one_earlier_chats_notebook(
    db_session, make_owner
):
    owner = await make_owner()
    first = await _standalone(db_session, owner, title="First")
    second = await _standalone(db_session, owner, title="Second")

    notebook_id = await place_thread(db_session, first)
    again = await place_thread(db_session, second)

    assert notebook_id == again
    notebook = await db_session.get(Notebook, notebook_id)
    assert notebook.title == EARLIER_CHATS_TITLE
    assert notebook.workspace_id == owner.workspace.id
    # The chat keeps its messages and now lists in the notebook.
    messages = (
        await db_session.scalars(
            select(NewChatMessage).where(NewChatMessage.thread_id == first.id)
        )
    ).all()
    assert len(messages) == 1
    listed = await threads.list_notebook_threads(
        owner.workspace.id, notebook_id, Response(), db_session, owner.auth
    )
    assert {t.id for t in listed} == {first.id, second.id}
    recorded = (
        await db_session.scalars(
            select(NotebookEarlierChat.thread_id).where(
                NotebookEarlierChat.notebook_id == notebook_id
            )
        )
    ).all()
    assert set(recorded) == {first.id, second.id}


async def test_a_notebook_chat_stays_in_its_own_notebook(db_session, make_owner):
    owner = await make_owner()
    notebook = Notebook(workspace_id=owner.workspace.id, title="Research")
    db_session.add(notebook)
    await db_session.flush()
    thread = await _standalone(db_session, owner, notebook_id=notebook.id)

    assert await place_thread(db_session, thread) == notebook.id
    assert (
        await db_session.scalar(
            select(NotebookEarlierChat).where(
                NotebookEarlierChat.thread_id == thread.id
            )
        )
        is None
    )


async def test_api_run_threads_are_never_filed(db_session, make_owner):
    owner = await make_owner()
    thread = await _standalone(db_session, owner, source=ARTIFACT_API_SOURCE)

    assert await place_thread(db_session, thread) is None
    assert thread.notebook_id is None


async def test_a_copied_public_chat_opens_in_a_notebook(db_session, make_owner):
    sharer = await make_owner()
    source = await _standalone(db_session, sharer, title="Shared")
    token = secrets.token_urlsafe(16)
    db_session.add(
        PublicChatSnapshot(
            thread_id=source.id,
            workspace_id=source.workspace_id,
            share_token=token,
            content_hash=secrets.token_hex(16),
            snapshot_data={
                "title": "Shared",
                "messages": [
                    {"role": "user", "content": [{"type": "text", "text": "hi"}]}
                ],
            },
            message_ids=[],
            created_by_user_id=sharer.user.id,
        )
    )
    await db_session.flush()
    reader = await make_owner()

    copied = await clone_from_snapshot(db_session, token, reader.user)

    assert copied["workspace_id"] == reader.workspace.id
    thread = await db_session.get(NewChatThread, copied["thread_id"])
    assert thread.notebook_id == copied["notebook_id"]
    notebook = await db_session.get(Notebook, copied["notebook_id"])
    assert notebook.title == EARLIER_CHATS_TITLE
    assert notebook.workspace_id == reader.workspace.id
