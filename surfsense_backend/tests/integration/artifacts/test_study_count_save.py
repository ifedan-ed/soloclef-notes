"""A headless flashcards run saves the number of cards the request asked for."""

from __future__ import annotations

import json
from contextlib import asynccontextmanager
from unittest.mock import AsyncMock

import pytest
from langchain.tools import ToolRuntime

from app.agents.chat.multi_agent_chat.subagents.builtins.deliverables.tools import (
    save_artifact as save_artifact_tool,
)
from app.artifacts import service
from app.artifacts.persistence import Artifact
from app.artifacts.verification import service as verify_service
from app.artifacts.verification.save_attempts import clear_refused_saves
from app.db import ChatVisibility, Document, NewChatThread
from app.deliverables.artifact.item_count import ItemCount
from tests.utils.fake_sandbox import FakeSandboxSession

from .test_service import MemoryBackend

pytestmark = pytest.mark.integration

SECRET = "test-secret"
PATH = "/workspace/deck.json"


def _deck(n: int) -> bytes:
    return json.dumps(
        {
            "schema_version": 1,
            "title": "P values",
            "cards": [
                {"front_text": f"Question {i}", "back_text": f"Answer {i}"}
                for i in range(1, n + 1)
            ],
        }
    ).encode()


@pytest.fixture
async def save_deck(db_session, db_workspace, db_user, monkeypatch):
    thread = NewChatThread(
        title="Study counts",
        workspace_id=db_workspace.id,
        created_by_id=db_user.id,
        visibility=ChatVisibility.PRIVATE,
    )
    db_session.add(thread)
    await db_session.flush()
    sandbox = FakeSandboxSession()

    class Registry:
        async def get_session(self, _thread_id, _workspace_id):
            return sandbox

    async def get_registry():
        return Registry()

    @asynccontextmanager
    async def session_context():
        yield db_session

    monkeypatch.setattr(service, "get_storage_backend", lambda *_: MemoryBackend())
    monkeypatch.setattr(
        service, "knowledge_store_enabled_for", AsyncMock(return_value=False)
    )
    monkeypatch.setattr(save_artifact_tool, "get_registry", get_registry)
    monkeypatch.setattr(save_artifact_tool, "shielded_async_session", session_context)
    monkeypatch.setattr(save_artifact_tool.app_config, "SECRET_KEY", SECRET)
    clear_refused_saves(sandbox.session_id, PATH)
    runtime = ToolRuntime(
        state={},
        context=None,
        config={"configurable": {"thread_id": f"{thread.id}::task:flashcards"}},
        stream_writer=None,
        tool_call_id="flashcards",
        store=None,
    )

    async def save(cards: int, item_count: ItemCount | None):
        sandbox.files[PATH] = _deck(cards)
        verified = await verify_service.verify_artifact(
            sandbox,
            PATH,
            format="flashcards",
            workspace_id=db_workspace.id,
            vision_llm=None,
            secret_key=SECRET,
        )
        assert verified.verified, verified.findings
        tool = save_artifact_tool.create_save_artifact_tool(
            db_workspace.id, item_count=item_count
        )
        command = await tool.coroutine(title="P values", path=PATH, runtime=runtime)
        return json.loads(command.update["messages"][0].content)

    return save


async def _saved_card_count(db_session, saved) -> int:
    artifact = await db_session.get(Artifact, saved["artifact_id"])
    document = await db_session.get(Document, artifact.document_id)
    return document.source_markdown.count("## Card ")


async def test_fewer_saves_ten_cards_when_the_model_wrote_eighteen(
    save_deck, db_session
):
    saved = await save_deck(18, ItemCount(target=10, minimum=9))

    assert "artifact_id" in saved, saved
    assert await _saved_card_count(db_session, saved) == 10


async def test_too_few_cards_are_refused_with_the_numbers(save_deck):
    refused = await save_deck(6, ItemCount(target=10, minimum=9))

    assert refused["status"] == "failed"
    assert "has 6 cards" in refused["error"] and "asked for 10" in refused["error"]


async def test_a_chat_save_without_a_count_is_unchanged(save_deck, db_session):
    saved = await save_deck(18, None)

    assert await _saved_card_count(db_session, saved) == 18
