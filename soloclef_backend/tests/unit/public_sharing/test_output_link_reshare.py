"""Sharing an unchanged output again returns its link with the title it has now."""

from __future__ import annotations

from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest

import app.public_sharing.output_links as output_links

pytestmark = pytest.mark.unit


@pytest.fixture
def renamed_output(monkeypatch):
    async def allowed(*_args, **_kwargs):
        return None

    async def ready(*_args, **_kwargs):
        output = SimpleNamespace(id=78, title="The eight planets", kind="slides")
        artifact = SimpleNamespace(
            id=114,
            generation=1,
            format="pptx",
            document=SimpleNamespace(source_markdown="# Planets", content=""),
        )
        return output, artifact

    async def no_file(*_args, **_kwargs):
        return None

    monkeypatch.setattr(output_links, "check_permission", allowed)
    monkeypatch.setattr(output_links, "_ready_artifact", ready)
    monkeypatch.setattr(output_links, "_primary_file", no_file)


@pytest.mark.asyncio
async def test_reshare_after_a_rename_keeps_the_link_and_takes_the_new_title(
    renamed_output,
):
    existing = SimpleNamespace(
        id=11,
        share_token="tok",
        expires_at=None,
        snapshot_data={
            "kind": "output",
            "title": "Planets fact sheet",
            "artifact_ids": [114],
            "output": {"title": "Planets fact sheet", "artifact_id": 114},
        },
    )
    session = AsyncMock()
    session.scalar.return_value = existing
    notebook = SimpleNamespace(id=70, workspace_id=6)

    link = await output_links.create_output_snapshot(
        session, SimpleNamespace(user=SimpleNamespace(id="u")), notebook, 78, 7
    )

    assert link["snapshot_id"] == 11
    assert link["is_new"] is False
    assert existing.snapshot_data["title"] == "The eight planets"
    assert existing.snapshot_data["output"]["title"] == "The eight planets"
    assert existing.snapshot_data["artifact_ids"] == [114]
    session.commit.assert_awaited()
