"""A mind map saves the Markdown that was verified, and a refused save cannot loop.

The run that looped thirteen times failed because save compared the model's
retyped Markdown with the verified file. The sandbox, database and storage
are the real save path; only the sandbox registry is a fake.
"""

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
from app.artifacts.verification.save_attempts import (
    MAX_REFUSED_SAVES,
    SaveAttemptsExhaustedError,
    clear_refused_saves,
)
from app.db import ChatVisibility, Document, NewChatThread
from tests.utils.fake_sandbox import FakeSandboxSession

from .test_mindmap_artifacts import _mindmap_png
from .test_service import MemoryBackend

pytestmark = pytest.mark.integration

SECRET = "test-secret"
MARKDOWN_PATH = "/workspace/loop.md"
PRIMARY_PATH = "/workspace/loop.png"
# As a Python heredoc writes it: trailing newline, curly quotes, an en dash.
VERIFIED = (
    "# P values – a dirty dozen\n\n- “Significant”\n  - Not proof\n- Effect size\n"  # noqa: RUF001
)


@pytest.fixture
async def mindmap(db_session, db_workspace, db_user, monkeypatch):
    thread = NewChatThread(
        title="Mind-map loop",
        workspace_id=db_workspace.id,
        created_by_id=db_user.id,
        visibility=ChatVisibility.PRIVATE,
    )
    db_session.add(thread)
    await db_session.flush()
    sandbox = FakeSandboxSession(
        {MARKDOWN_PATH: VERIFIED.encode(), PRIMARY_PATH: _mindmap_png()}
    )

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

    async def verify():
        result = await verify_service.verify_artifact(
            sandbox,
            PRIMARY_PATH,
            format="mindmap",
            workspace_id=db_workspace.id,
            vision_llm=None,
            markdown_path=MARKDOWN_PATH,
            secret_key=SECRET,
        )
        assert result.verified

    runtime = ToolRuntime(
        state={},
        context=None,
        config={"configurable": {"thread_id": f"{thread.id}::task:mindmap"}},
        stream_writer=None,
        tool_call_id="mindmap",
        store=None,
    )
    tool = save_artifact_tool.create_save_artifact_tool(db_workspace.id)
    clear_refused_saves(sandbox.session_id, PRIMARY_PATH)

    async def save(markdown):
        kwargs = {"title": "P values", "path": PRIMARY_PATH, "runtime": runtime}
        if markdown is not None:
            kwargs["markdown_representation"] = markdown
        command = await tool.coroutine(**kwargs)
        return json.loads(command.update["messages"][0].content)

    return sandbox, verify, save


@pytest.mark.parametrize(
    "retyped",
    [
        VERIFIED.rstrip("\n"),  # the trailing newline dropped
        VERIFIED.replace("“", '"').replace("”", '"'),  # straight quotes
        VERIFIED.replace("–", "-"),  # the dash re-typed  # noqa: RUF001
        None,  # not passed at all
    ],
    ids=["no-trailing-newline", "straight-quotes", "hyphen", "omitted"],
)
async def test_the_verified_markdown_is_saved_whatever_the_model_retyped(
    mindmap, db_session, retyped
):
    _sandbox, verify, save = mindmap
    await verify()

    saved = await save(retyped)

    assert "artifact_id" in saved, saved
    artifact = await db_session.get(Artifact, saved["artifact_id"])
    document = await db_session.get(Document, artifact.document_id)
    assert document.source_markdown == VERIFIED


async def test_markdown_edited_after_verification_is_still_refused(mindmap):
    sandbox, verify, save = mindmap
    await verify()
    sandbox.files[MARKDOWN_PATH] = b"# Something else\n\n- entirely\n"

    refused = await save("# Something else\n\n- entirely\n")

    assert refused["status"] == "failed"
    assert "changed after verification" in refused["error"]


async def test_repeated_refusals_stop_the_run_with_the_reason(mindmap):
    sandbox, verify, save = mindmap
    for attempt in range(1, MAX_REFUSED_SAVES + 1):
        sandbox.files[MARKDOWN_PATH] = VERIFIED.encode()
        await verify()
        sandbox.files[MARKDOWN_PATH] = f"# Edited {attempt}\n\n- again\n".encode()
        if attempt < MAX_REFUSED_SAVES:
            assert (await save(None))["status"] == "failed"
        else:
            with pytest.raises(SaveAttemptsExhaustedError, match="refused 3 times"):
                await save(None)


async def test_a_successful_save_resets_the_count(mindmap):
    sandbox, verify, save = mindmap
    for _ in range(MAX_REFUSED_SAVES - 1):
        sandbox.files[MARKDOWN_PATH] = VERIFIED.encode()
        await verify()
        sandbox.files[MARKDOWN_PATH] = b"# Edited\n\n- again\n"
        assert (await save(None))["status"] == "failed"
    sandbox.files[MARKDOWN_PATH] = VERIFIED.encode()
    await verify()
    assert "artifact_id" in await save(None)

    # Had the count survived the save, this third refusal would stop the run.
    await verify()
    sandbox.files[MARKDOWN_PATH] = b"# Edited\n\n- again\n"
    last = await save(None)
    assert last["status"] == "failed", last
