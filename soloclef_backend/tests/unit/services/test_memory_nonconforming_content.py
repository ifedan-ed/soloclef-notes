"""Content the memory document cannot hold is refused, never silently emptied.

``save_memory`` parsed the submitted markdown and stored the *render* of what it
understood. Content it understood nothing of rendered to the empty string, so a
short piece of plain prose replaced the whole document with ``""`` and the route
answered 200 -- data loss with a success code. The same prose one character
longer was refused by ``validate_heading_sanity``, whose exemption for anything
of forty characters or fewer is where the two answers parted.

The exemption is gone: content with no heading and no bullet the document can
hold is refused whatever its length, and the stored document is left alone.
"""

from __future__ import annotations

from types import SimpleNamespace

import pytest

from app.services.memory import MemoryScope, reset_memory, save_memory

pytestmark = pytest.mark.unit

STORED = "## Preferences\n- 2026-01-02: prefers short answers"
REFUSAL = "Memory must be markdown with at least one ## heading."


class _FakeSession:
    def __init__(self) -> None:
        self.commit_calls = 0
        self.added: list = []

    def add(self, obj) -> None:
        self.added.append(obj)

    async def commit(self) -> None:
        self.commit_calls += 1

    async def rollback(self) -> None:
        pass


@pytest.fixture
def world(monkeypatch):
    target = SimpleNamespace(memory_md=STORED, shared_memory_md=STORED)
    session = _FakeSession()

    async def fake_load_target(**_kwargs):
        return target

    monkeypatch.setattr("app.services.memory.service._load_target", fake_load_target)
    return target, session


async def _save(session, content, scope=MemoryScope.USER):
    return await save_memory(
        scope=scope,
        target_id="00000000-0000-0000-0000-000000000000",
        content=content,
        session=session,
    )


@pytest.mark.parametrize(
    "content",
    [
        "short prose",
        "1234567890123456789012345678901234567890",
        "12345678901234567890123456789012345678901",
        "- (2026-01-01) [fact] ",
        "NO_UPDATED",
        "preamble\nNO_UPDATE",
    ],
)
async def test_content_the_document_cannot_hold_is_refused(world, content) -> None:
    target, session = world

    result = await _save(session, content)

    assert result.status == "error"
    assert result.message == REFUSAL
    assert result.memory_md == STORED
    assert target.memory_md == STORED
    assert session.commit_calls == 0


async def test_the_refusal_does_not_depend_on_length(world) -> None:
    """The forty-character cliff is what made the behaviour incoherent."""
    _, session = world

    under = await _save(session, "x" * 40)
    over = await _save(session, "x" * 41)

    assert under.status == over.status == "error"
    assert under.message == over.message == REFUSAL


@pytest.mark.parametrize(
    ("content", "expected"),
    [
        ("## Facts\n- 2026-05-19: a heading and a bullet", "## Facts"),
        ("- 2026-05-19: a canonical bullet with no heading", "## Memory"),
        ("- (2026-05-19) [fact] a legacy bullet", "## Memory"),
    ],
)
async def test_conforming_content_still_saves(world, content, expected) -> None:
    target, session = world

    result = await _save(session, content)

    assert result.status == "saved"
    assert target.memory_md.startswith(expected)


async def test_emptying_the_document_on_purpose_still_works(world) -> None:
    """An empty submission is a clear, not a failed parse -- and reset is one."""
    target, session = world

    cleared = await _save(session, "   \n\t  ")
    assert cleared.status == "saved"
    assert target.memory_md == ""

    target.memory_md = STORED
    reset = await reset_memory(
        scope=MemoryScope.USER,
        target_id="00000000-0000-0000-0000-000000000000",
        session=session,
    )
    assert reset.status == "saved"
    assert target.memory_md == ""


async def test_a_sentinel_is_still_a_no_op(world) -> None:
    target, session = world

    result = await _save(session, "NO_UPDATE")

    assert result.status == "no_op"
    assert target.memory_md == STORED
