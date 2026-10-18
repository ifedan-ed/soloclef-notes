"""Public links: Owners create them, they expire, they can be revoked, and
they carry nothing internal. Over HTTP on real Postgres."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest

from app.db import (
    ChatVisibility,
    NewChatMessage,
    NewChatMessageRole,
    NewChatThread,
    PublicChatSnapshot,
)

pytestmark = pytest.mark.integration


async def _chat(db_session, team, author, visibility=ChatVisibility.SEARCH_SPACE):
    thread = NewChatThread(
        title="Asthma follow-up",
        workspace_id=team.ws,
        created_by_id=author.id,
        visibility=visibility,
    )
    db_session.add(thread)
    await db_session.flush()
    db_session.add_all(
        [
            NewChatMessage(
                thread_id=thread.id,
                role=NewChatMessageRole.USER,
                content=[{"type": "text", "text": "Summarise the guideline"}],
                author_id=author.id,
            ),
            NewChatMessage(
                thread_id=thread.id,
                role=NewChatMessageRole.ASSISTANT,
                content=[
                    {"type": "text", "text": "Here it is [citation:3]."},
                    {
                        "type": "tool-call",
                        "toolName": "generate_image",
                        "toolCallId": "call-1",
                        "args": {"prompt": "chart"},
                        "result": {
                            "status": "ready",
                            "artifact_id": 41,
                            "workspace_id": team.ws,
                            "storage_key": "artifacts/secret/key.png",
                            "file_path": "/srv/data/key.png",
                        },
                    },
                    {
                        "type": "tool-call",
                        "toolName": "search_knowledge_base",
                        "toolCallId": "call-2",
                        "args": {"query": "patient 123"},
                        "result": {"documents": ["internal"]},
                    },
                ],
            ),
        ]
    )
    await db_session.flush()
    return thread


def _share(thread_id):
    return f"/api/v1/threads/{thread_id}/snapshots"


async def test_only_owners_create_public_links_by_default(db_session, client, team):
    thread = await _chat(db_session, team, team.editor)
    editor = await client.act_as(team.editor).post(_share(thread.id), json={})
    assert editor.status_code == 403
    viewer = await client.act_as(team.viewer).post(_share(thread.id), json={})
    assert viewer.status_code == 403
    outsider = await client.act_as(team.outsider).post(_share(thread.id), json={})
    assert outsider.status_code == 403
    owner = await client.act_as(team.owner).post(_share(thread.id), json={})
    assert owner.status_code == 200, owner.text


async def test_expiry_defaults_to_seven_days_and_can_be_thirty_or_never(
    db_session, client, team
):
    thread = await _chat(db_session, team, team.owner)
    client.act_as(team.owner)
    default = (await client.post(_share(thread.id))).json()
    expires = datetime.fromisoformat(default["expires_at"])
    assert (
        timedelta(days=6, hours=23) < expires - datetime.now(UTC) <= timedelta(days=7)
    )
    # Same content keeps its URL; the newest choice of expiry applies.
    thirty = (await client.post(_share(thread.id), json={"expires_in_days": 30})).json()
    assert thirty["share_token"] == default["share_token"]
    assert datetime.fromisoformat(thirty["expires_at"]) - datetime.now(UTC) > timedelta(
        days=29
    )
    never = (
        await client.post(_share(thread.id), json={"expires_in_days": None})
    ).json()
    assert never["expires_at"] is None
    odd = await client.post(_share(thread.id), json={"expires_in_days": 3})
    assert odd.status_code == 422


async def test_owner_cannot_publish_someone_elses_private_chat(
    db_session, client, team
):
    private = await _chat(db_session, team, team.editor, ChatVisibility.PRIVATE)
    response = await client.act_as(team.owner).post(_share(private.id), json={})
    assert response.status_code == 403


async def test_anyone_with_the_link_reads_a_redacted_copy(db_session, client, team):
    thread = await _chat(db_session, team, team.owner)
    token = (await client.act_as(team.owner).post(_share(thread.id), json={})).json()[
        "share_token"
    ]

    page = await client.act_as(None).get(f"/api/v1/public/{token}")
    assert page.status_code == 200
    body = page.json()
    assert body["kind"] == "chat"
    assert body["expires_at"] is not None
    text = str(body)
    for internal in (
        str(team.owner.id),
        "storage_key",
        "/srv/data",
        "search_knowledge_base",
        "patient 123",
        "citation:",
        team.owner.email,
    ):
        assert internal not in text
    user_message = body["messages"][0]
    assert user_message["author"] == {"display_name": "Owner", "avatar_url": None}
    image = body["messages"][1]["content"][1]
    assert image["result"]["artifact_id"] == 41


async def test_expired_links_answer_gone_everywhere(db_session, client, team):
    thread = await _chat(db_session, team, team.owner)
    created = (await client.act_as(team.owner).post(_share(thread.id), json={})).json()
    snapshot = await db_session.get(PublicChatSnapshot, created["snapshot_id"])
    snapshot.expires_at = datetime.now(UTC) - timedelta(seconds=1)
    await db_session.flush()

    client.act_as(None)
    page = await client.get(f"/api/v1/public/{created['share_token']}")
    assert page.status_code == 410
    artifact = await client.get(
        f"/api/v1/public/{created['share_token']}/artifacts/41/content"
    )
    assert artifact.status_code == 404
    clone = await client.act_as(team.viewer).post(
        f"/api/v1/public/{created['share_token']}/clone"
    )
    assert clone.status_code == 404

    # Sharing the same content again makes a new, live link.
    fresh = (await client.act_as(team.owner).post(_share(thread.id), json={})).json()
    assert fresh["share_token"] != created["share_token"]
    assert (
        await client.get(f"/api/v1/public/{fresh['share_token']}")
    ).status_code == 200


async def test_revoked_links_are_gone(db_session, client, team):
    thread = await _chat(db_session, team, team.owner)
    created = (await client.act_as(team.owner).post(_share(thread.id), json={})).json()
    revoke = f"/api/v1/workspaces/{team.ws}/snapshots/{created['snapshot_id']}"

    assert (await client.act_as(team.editor).delete(revoke)).status_code == 403
    assert (await client.act_as(team.outsider).delete(revoke)).status_code == 403
    assert (await client.act_as(team.owner).delete(revoke)).status_code == 200
    page = await client.act_as(None).get(f"/api/v1/public/{created['share_token']}")
    assert page.status_code == 404


async def test_public_links_page_lists_creator_and_expiry_by_role(
    db_session, client, team
):
    shared = await _chat(db_session, team, team.owner)
    private = await _chat(db_session, team, team.owner, ChatVisibility.PRIVATE)
    client.act_as(team.owner)
    await client.post(_share(shared.id), json={"expires_in_days": 30})
    await client.post(_share(private.id), json={})

    listing = f"/api/v1/workspaces/{team.ws}/snapshots"
    as_owner = (await client.get(listing)).json()["snapshots"]
    assert len(as_owner) == 2
    first = as_owner[0]
    assert first["created_by_name"] == "Owner"
    assert first["expires_at"] and first["is_expired"] is False
    assert first["kind"] == "chat"

    # A Viewer sees the link to the shared chat, not to the Owner's private one.
    as_viewer = (await client.act_as(team.viewer).get(listing)).json()["snapshots"]
    assert [s["thread_id"] for s in as_viewer] == [shared.id]

    assert (await client.act_as(team.outsider).get(listing)).status_code == 403


async def test_shared_files_that_a_browser_would_run_are_downloads(
    db_session, client, team, monkeypatch
):
    from app.artifacts.persistence import Artifact, ArtifactFile, ArtifactFileRole
    from app.db import Document, DocumentType
    from app.file_storage import factory

    class Store:
        async def exists(self, key):
            return True

        async def open_stream(self, key):
            yield b"<script>alert(1)</script>"

    monkeypatch.setattr(factory, "get_storage_backend", lambda *_: Store())
    document = Document(
        title="Report",
        document_type=DocumentType.ARTIFACT,
        content="# Report",
        source_markdown="# Report",
        content_hash="h-report",
        workspace_id=team.ws,
        status={"state": "ready"},
    )
    db_session.add(document)
    await db_session.flush()
    artifact = Artifact(document_id=document.id, workspace_id=team.ws, format="html")
    db_session.add(artifact)
    await db_session.flush()
    db_session.add(
        ArtifactFile(
            artifact_id=artifact.id,
            role=ArtifactFileRole.PRIMARY,
            storage_backend="local",
            storage_key="k",
            original_filename="report.html",
            mime_type="text/html",
            size_bytes=25,
            checksum_sha256="0" * 64,
        )
    )
    thread = await _chat(db_session, team, team.owner)
    db_session.add(
        PublicChatSnapshot(
            thread_id=thread.id,
            workspace_id=team.ws,
            share_token="html-share",
            content_hash="html",
            snapshot_data={"artifact_ids": [artifact.id], "messages": []},
            message_ids=[],
        )
    )
    await db_session.flush()

    response = await client.act_as(None).get(
        f"/api/v1/public/html-share/artifacts/{artifact.id}/content"
    )
    assert response.status_code == 200
    assert response.headers["content-disposition"].startswith("attachment")
    assert response.headers["content-security-policy"] == "sandbox"
    assert response.headers["x-content-type-options"] == "nosniff"
