"""Live notebook links over HTTP on real Postgres.

The link shows one notebook as it is now (chat, finished outputs, sources) to
anyone holding it, and nothing else: not another notebook's outputs or
sources, not the workspace, not account details, not after it expires, is
revoked, or its notebook is deleted.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest
from sqlalchemy import delete, select

from app.db import (
    ChatVisibility,
    Chunk,
    DeliverableJobStatus,
    NewChatMessage,
    NewChatMessageRole,
    NewChatThread,
    WorkspaceMembership,
)
from app.file_storage import service as file_service
from app.file_storage.persistence.enums import DocumentFileKind
from app.notebooks.persistence import Notebook, NotebookSource
from app.public_sharing.notebook_links.models import NotebookPublicLink
from tests.utils.acting_client import acting_client
from tests.utils.workspace_team import join, new_user, role_named

pytestmark = pytest.mark.integration

PDF = b"%PDF-1.4\n" + b"0123456789" * 50 + b"\n%%EOF"


def _link(ws, nb):
    return f"/api/v1/workspaces/{ws}/notebooks/{nb}/public-link"


def _public(token, rest=""):
    return f"/api/v1/public-notebooks/{token}{rest}"


@pytest.fixture
def stored(artifact_store, monkeypatch):
    """Document files on the same throwaway disk as artifacts."""
    monkeypatch.setattr(file_service, "get_storage_backend", lambda *_: artifact_store)

    async def put(db_session, document, data=PDF, filename="guideline.pdf", **kw):
        record = await file_service.store_document_file(
            db_session,
            document_id=document.id,
            workspace_id=document.workspace_id,
            data=data,
            filename=filename,
            mime_type=kw.pop("mime_type", "application/pdf"),
            backend=artifact_store,
            **kw,
        )
        await db_session.flush()
        return record

    return put


async def _chunk_of(db_session, document):
    return await db_session.scalar(
        select(Chunk.id).where(Chunk.document_id == document.id)
    )


async def _chat(db_session, notebook, author, parts, *, user_text="Summarise it"):
    thread = NewChatThread(
        title="Chat",
        workspace_id=notebook.workspace_id,
        created_by_id=author.id,
        visibility=ChatVisibility.PRIVATE,
        notebook_id=notebook.id,
    )
    db_session.add(thread)
    await db_session.flush()
    await _say(db_session, thread, author, user_text, parts)
    return thread


async def _say(db_session, thread, author, user_text, parts):
    db_session.add(
        NewChatMessage(
            thread_id=thread.id,
            role=NewChatMessageRole.USER,
            content=[{"type": "text", "text": user_text}],
            author_id=author.id,
        )
    )
    await db_session.flush()
    db_session.add(
        NewChatMessage(
            thread_id=thread.id, role=NewChatMessageRole.ASSISTANT, content=parts
        )
    )
    await db_session.flush()


@pytest.fixture
def world(
    db_session, make_owner, make_notebook, make_document, make_studio_output, stored
):
    """An Owner's notebook with a PDF source, a chat that cites it, a finished
    report and a running quiz; a second notebook in the same workspace with its
    own source and output; and a stranger's workspace."""

    async def build():
        owner = await make_owner()
        ws = owner.workspace.id
        source = await make_document(ws, title="Asthma guideline", text="Use a spacer.")
        pdf = await stored(db_session, source)
        other_doc = await make_document(ws, title="Other notebook's doc", text="x")
        other_pdf = await stored(db_session, other_doc, filename="other.pdf")
        unlisted = await make_document(ws, title="Workspace-only doc", text="y")
        notebook = await make_notebook(
            ws, title="Ward 4 asthma", sources=[("document", source.id)]
        )
        notebook.icon = "🫁"
        other = await make_notebook(
            ws, title="Private notebook", sources=[("document", other_doc.id)]
        )
        report = await make_studio_output(
            notebook,
            kind="report",
            artifact_format="html",
            files=(
                ("report.html", "text/html", b"<h1>Report</h1><script>x()</script>"),
            ),
            title="Discharge teaching",
        )
        running = await make_studio_output(
            notebook,
            kind="quiz",
            artifact_format="quiz",
            status=DeliverableJobStatus.RUNNING,
            title="Quiz in progress",
        )
        failed = await make_studio_output(
            notebook,
            kind="flashcards",
            artifact_format="flashcards",
            status=DeliverableJobStatus.FAILED,
            title="Failed cards",
        )
        foreign_output = await make_studio_output(
            other,
            kind="report",
            artifact_format="pdf",
            files=(("secret.pdf", "application/pdf", PDF),),
            title="Other notebook's report",
        )
        chunk = await _chunk_of(db_session, source)
        other_chunk = await _chunk_of(db_session, unlisted)
        thread = await _chat(
            db_session,
            notebook,
            owner.user,
            [
                {"type": "data-activities", "data": {"steps": ["searching"]}},
                {
                    "type": "tool-call",
                    "toolName": "search_knowledge_base",
                    "toolCallId": "c1",
                    "args": {"query": "patient 123"},
                    "result": {"documents": ["internal"]},
                },
                {
                    "type": "text",
                    "text": (
                        f"Use a spacer [citation:{chunk}] and see "
                        f"[citation:{other_chunk}] [citation:doc-4]."
                    ),
                },
            ],
        )
        stranger = await make_owner()
        stranger_doc = await make_document(stranger.workspace.id, title="Stranger doc")
        stranger_pdf = await stored(db_session, stranger_doc, filename="s.pdf")
        return {
            "owner": owner,
            "ws": ws,
            "notebook": notebook,
            "other": other,
            "source": source,
            "pdf": pdf,
            "other_doc": other_doc,
            "other_pdf": other_pdf,
            "unlisted": unlisted,
            "report": report,
            "running": running,
            "failed": failed,
            "foreign_output": foreign_output,
            "chunk": chunk,
            "thread": thread,
            "stranger": stranger,
            "stranger_doc": stranger_doc,
            "stranger_pdf": stranger_pdf,
        }

    return build


async def _share(client, w, **body):
    response = await client.act_as(w["owner"].user).post(
        _link(w["ws"], w["notebook"].id), json=body
    )
    assert response.status_code == 200, response.text
    return response.json()


async def test_only_owners_share_and_the_link_has_expiry_and_one_per_notebook(
    db_session, world
):
    w = await world()
    ws, nb = w["ws"], w["notebook"].id
    editor = await new_user(db_session, "editor")
    viewer = await new_user(db_session, "viewer")
    await join(db_session, ws, editor, await role_named(db_session, ws, "Editor"))
    await join(db_session, ws, viewer, await role_named(db_session, ws, "Viewer"))
    async with acting_client(db_session) as client:
        for who in (editor, viewer, w["stranger"].user):
            denied = await client.act_as(who).post(_link(ws, nb), json={})
            assert denied.status_code == 403, who.email
        assert (
            await client.act_as(None).post(_link(ws, nb), json={})
        ).status_code == 401

        first = await _share(client, w)
        assert first["public_url"].endswith(f"/public/notebook/{first['share_token']}")
        left = datetime.fromisoformat(first["expires_at"]) - datetime.now(UTC)
        assert timedelta(days=6, hours=23) < left <= timedelta(days=7)

        thirty = await _share(client, w, expires_in_days=30)
        assert thirty["share_token"] == first["share_token"]
        assert thirty["id"] == first["id"]
        never = await _share(client, w, expires_in_days=None)
        assert never["expires_at"] is None
        odd = await client.post(_link(ws, nb), json={"expires_in_days": 3})
        assert odd.status_code == 422

        again = await _share(client, w, regenerate=True, expires_in_days=7)
        assert again["share_token"] != first["share_token"]
        assert (
            await client.act_as(None).get(_public(first["share_token"]))
        ).status_code == 404
        assert (await client.get(_public(again["share_token"]))).status_code == 200
        count = await db_session.scalar(
            select(NotebookPublicLink.id).where(NotebookPublicLink.notebook_id == nb)
        )
        assert count == again["id"]

        # Members see the link (for the badge); outsiders do not.
        state = await client.act_as(viewer).get(_link(ws, nb))
        assert state.json()["link"]["share_token"] == again["share_token"]
        assert state.json()["link"]["can_revoke"] is False
        assert (
            await client.act_as(w["stranger"].user).get(_link(ws, nb))
        ).status_code == 403


async def test_a_visitor_sees_the_notebook_and_nothing_internal(db_session, world):
    w = await world()
    w["notebook"].chat_instructions = "SYSTEM: never mention the ward budget"
    await db_session.flush()
    async with acting_client(db_session) as client:
        token = (await _share(client, w))["share_token"]
        response = await client.act_as(None).get(_public(token))
    assert response.status_code == 200
    assert response.headers["cache-control"] == "no-store"
    assert response.headers["x-robots-tag"].startswith("noindex")
    body = response.json()
    assert body["notebook"] == {"title": "Ward 4 asthma", "icon": "🫁"}

    user, assistant = body["messages"]
    assert user == {
        "role": "user",
        "content": [{"type": "text", "text": "Summarise it"}],
        "created_at": user["created_at"],
    }
    # Only the words; the cited source is renumbered, the rest dropped.
    assert assistant["content"] == [
        {"type": "text", "text": "Use a spacer [citation:1] and see."}
    ]
    assert body["citations"] == {
        "1": {"source_id": w["source"].id, "title": "Asthma guideline", "page": None}
    }

    assert [o["title"] for o in body["outputs"]] == ["Discharge teaching"]
    assert body["outputs"][0]["artifact_id"] == w["report"].artifact.id
    assert body["sources"] == [
        {
            "id": w["source"].id,
            "title": "Asthma guideline",
            "document_type": "FILE",
            "has_file": True,
        }
    ]

    text = response.text
    owner = w["owner"].user
    for secret in (
        str(owner.id),
        owner.email,
        "storage_key",
        "artifacts/",
        "search_knowledge_base",
        "patient 123",
        "data-activities",
        "ward budget",
        f"citation:{w['chunk']}",
        "Private notebook",
        "Other notebook",
        "Workspace-only doc",
        "Quiz in progress",
        "Failed cards",
    ):
        assert secret not in text, secret


async def test_new_turns_and_outputs_appear_without_sharing_again(
    db_session, world, make_studio_output
):
    w = await world()
    async with acting_client(db_session) as client:
        token = (await _share(client, w))["share_token"]
        client.act_as(None)
        before = (await client.get(_public(token))).json()
        assert len(before["messages"]) == 2 and len(before["outputs"]) == 1

        await _say(
            db_session,
            w["thread"],
            w["owner"].user,
            "And for adults?",
            [{"type": "text", "text": "Same advice."}],
        )
        flashcards = await make_studio_output(
            w["notebook"],
            kind="flashcards",
            artifact_format="flashcards",
            files=(("cards.json", "application/json", b'{"cards": []}'),),
            title="Spacer cards",
        )
        # The running quiz finishes.
        w["running"].job.status = DeliverableJobStatus.READY
        w["running"].job.artifact_id = w["running"].artifact.id
        await db_session.flush()

        after = (await client.get(_public(token))).json()
    assert [m["content"][0]["text"] for m in after["messages"][2:]] == [
        "And for adults?",
        "Same advice.",
    ]
    assert {o["title"] for o in after["outputs"]} == {
        "Discharge teaching",
        "Spacer cards",
        "Quiz in progress",
    }
    assert flashcards.artifact.id in {o["artifact_id"] for o in after["outputs"]}


async def test_outputs_open_with_public_file_urls_and_download_by_title(
    db_session, world
):
    w = await world()
    artifact = w["report"].artifact
    async with acting_client(db_session) as client:
        token = (await _share(client, w))["share_token"]
        client.act_as(None)
        manifest = await client.get(
            _public(token, f"/artifacts/{artifact.id}/manifest")
        )
        assert manifest.status_code == 200
        data = manifest.json()
        assert data["document_id"] == 0
        assert "quiz_state" not in data and "flashcard_study_state" not in data
        [file] = data["files"]
        assert file["content_url"] == (
            f"/api/v1/public-notebooks/{token}/artifacts/{artifact.id}"
            f"/files/{file['file_id']}/content"
        )

        content = await client.get(file["content_url"])
        assert content.status_code == 200
        assert content.content.startswith(b"<h1>Report")
        # A file the browser would run is a sandboxed download.
        assert content.headers["content-disposition"].startswith("attachment")
        assert content.headers["content-security-policy"] == "sandbox"
        assert content.headers["x-content-type-options"] == "nosniff"

        download = await client.get(
            _public(token, f"/artifacts/{artifact.id}/download")
        )
        assert download.status_code == 200
        assert (
            'filename="Discharge teaching.html"'
            in download.headers["content-disposition"]
        )
        assert download.headers["content-security-policy"] == "sandbox"


async def test_other_notebooks_outputs_and_sources_are_out_of_reach(db_session, world):
    w = await world()
    async with acting_client(db_session) as client:
        token = (await _share(client, w))["share_token"]
        client.act_as(None)
        foreign = w["foreign_output"]
        foreign_file = foreign.files[0].id
        running = w["running"].artifact.id
        for path in (
            f"/artifacts/{foreign.artifact.id}/manifest",
            f"/artifacts/{foreign.artifact.id}/files/{foreign_file}/content",
            f"/artifacts/{foreign.artifact.id}/download",
            # Running and failed outputs are hidden, not just unlisted.
            f"/artifacts/{running}/manifest",
            f"/artifacts/{w['failed'].artifact.id}/manifest",
            # Another artifact's file id under an allowed artifact.
            f"/artifacts/{w['report'].artifact.id}/files/{foreign_file}/content",
            f"/sources/{w['other_doc'].id}",
            f"/sources/{w['other_doc'].id}/files/{w['other_pdf'].id}/content",
            f"/sources/{w['unlisted'].id}",
            f"/sources/{w['stranger_doc'].id}",
            f"/sources/{w['stranger_doc'].id}/files/{w['stranger_pdf'].id}/content",
            # An allowed source with another source's file id.
            f"/sources/{w['source'].id}/files/{w['other_pdf'].id}/content",
            f"/sources/{w['source'].id}/files/{w['stranger_pdf'].id}/content",
        ):
            response = await client.get(_public(token, path))
            assert response.status_code == 404, path

        # A token for the wrong notebook id changes nothing: ids come from the token.
        wrong = await client.get(f"/api/v1/public-notebooks/{token}/../{w['other'].id}")
        assert wrong.status_code == 404


async def test_path_traversal_and_malformed_ids_never_reach_storage(db_session, world):
    w = await world()
    async with acting_client(db_session) as client:
        token = (await _share(client, w))["share_token"]
        client.act_as(None)
        src = w["source"].id
        for path in (
            f"/sources/{src}/files/..%2F..%2F..%2Fetc%2Fpasswd/content",
            f"/sources/..%2F{src}/files/{w['pdf'].id}/content",
            f"/sources/{src}/files/{w['pdf'].id}%2F..%2F..%2Fsecret/content",
            "/sources/%2e%2e%2f%2e%2e%2fetc%2fpasswd",
            f"/artifacts/{w['report'].artifact.id}/files/-1/content",
            "/artifacts/1e3/manifest",
        ):
            response = await client.get(_public(token, path))
            assert response.status_code in (404, 422), (path, response.status_code)
            assert b"root:" not in response.content
        # The token itself cannot carry a path.
        assert (await client.get(_public("..%2F..%2Fetc"))).status_code == 404
        assert (await client.get(_public("x" * 300))).status_code == 404


async def test_a_source_pdf_opens_with_byte_ranges(db_session, world, stored):
    w = await world()
    async with acting_client(db_session) as client:
        token = (await _share(client, w))["share_token"]
        client.act_as(None)
        source = (await client.get(_public(token, f"/sources/{w['source'].id}"))).json()
        assert source["presentation"] == "original"
        assert source["markdown"] == ""
        url = source["file"]["content_url"]
        assert url == (
            f"/api/v1/public-notebooks/{token}/sources/{w['source'].id}"
            f"/files/{w['pdf'].id}/content"
        )
        whole = await client.get(url)
        assert whole.status_code == 200 and whole.content == PDF
        assert whole.headers["content-type"] == "application/pdf"
        assert whole.headers["cache-control"] == "private, no-cache"
        part = await client.get(url, headers={"Range": "bytes=0-7"})
        assert part.status_code == 206
        assert part.content == PDF[:8]
        assert part.headers["content-range"] == f"bytes 0-7/{len(PDF)}"
        bad = await client.get(url, headers={"Range": "bytes=999999-"})
        assert bad.status_code == 416

        # Derived copies (a redacted version) stay private.
        redacted = await stored(
            db_session,
            w["source"],
            filename="redacted.pdf",
            kind=DocumentFileKind.REDACTED,
        )
        hidden = await client.get(
            _public(token, f"/sources/{w['source'].id}/files/{redacted.id}/content")
        )
        assert hidden.status_code == 404


async def test_a_source_without_a_viewable_file_opens_as_text(
    db_session, world, make_document
):
    w = await world()
    web = await make_document(w["ws"], title="Web page", text="# Spacers\nUse one.")
    db_session.add(NotebookSource(notebook_id=w["notebook"].id, document_id=web.id))
    await db_session.flush()
    async with acting_client(db_session) as client:
        token = (await _share(client, w))["share_token"]
        view = await client.act_as(None).get(_public(token, f"/sources/{web.id}"))
    assert view.status_code == 200
    assert view.json()["presentation"] == "text"
    assert view.json()["markdown"].startswith("# Spacers")
    assert view.json()["file"] is None


async def test_expired_links_are_gone_everywhere(db_session, world):
    w = await world()
    async with acting_client(db_session) as client:
        created = await _share(client, w)
        token = created["share_token"]
        link = await db_session.get(NotebookPublicLink, created["id"])
        link.expires_at = datetime.now(UTC) - timedelta(seconds=1)
        await db_session.flush()
        client.act_as(None)
        assert (await client.get(_public(token))).status_code == 410
        artifact = w["report"].artifact.id
        for path in (
            f"/artifacts/{artifact}/manifest",
            f"/artifacts/{artifact}/files/{w['report'].files[0].id}/content",
            f"/sources/{w['source'].id}",
            f"/sources/{w['source'].id}/files/{w['pdf'].id}/content",
        ):
            assert (await client.get(_public(token, path))).status_code == 410, path

        # Sharing again makes a new address; the old one stays dead.
        fresh = await _share(client, w)
        assert fresh["share_token"] != token
        assert (await client.act_as(None).get(_public(token))).status_code == 404


async def test_revoked_links_are_gone_and_only_owners_or_creators_revoke(
    db_session, world
):
    w = await world()
    ws, nb = w["ws"], w["notebook"].id
    editor = await new_user(db_session, "editor")
    await join(db_session, ws, editor, await role_named(db_session, ws, "Editor"))
    async with acting_client(db_session) as client:
        created = await _share(client, w)
        token = created["share_token"]
        assert (await client.act_as(editor).delete(_link(ws, nb))).status_code == 403
        by_id = f"/api/v1/workspaces/{ws}/notebook-links/{created['id']}"
        assert (await client.act_as(editor).delete(by_id)).status_code == 403
        stranger = w["stranger"].user
        assert (await client.act_as(stranger).delete(by_id)).status_code == 403
        # Another workspace's id for the same link is not found.
        foreign = f"/api/v1/workspaces/{w['stranger'].workspace.id}/notebook-links/{created['id']}"
        assert (await client.act_as(stranger).delete(foreign)).status_code == 404

        assert (
            await client.act_as(w["owner"].user).delete(_link(ws, nb))
        ).status_code == 204
        page = await client.act_as(None).get(_public(token))
        assert page.status_code == 404
        file = await client.get(
            _public(token, f"/sources/{w['source'].id}/files/{w['pdf'].id}/content")
        )
        assert file.status_code == 404

        # From the Public links page, too.
        again = await _share(client, w)
        again_by_id = f"/api/v1/workspaces/{ws}/notebook-links/{again['id']}"
        assert (
            await client.act_as(w["owner"].user).delete(again_by_id)
        ).status_code == 204
        assert (
            await client.act_as(None).get(_public(again["share_token"]))
        ).status_code == 404


async def test_deleting_the_notebook_kills_the_link(db_session, world):
    w = await world()
    async with acting_client(db_session) as client:
        token = (await _share(client, w))["share_token"]
        await db_session.execute(
            delete(Notebook).where(Notebook.id == w["notebook"].id)
        )
        await db_session.flush()
        assert (await client.act_as(None).get(_public(token))).status_code == 404
    assert (
        await db_session.scalar(
            select(NotebookPublicLink).where(NotebookPublicLink.share_token == token)
        )
    ) is None


async def test_the_link_dies_when_its_creator_may_no_longer_share(db_session, world):
    w = await world()
    ws = w["ws"]
    co_owner = await new_user(db_session, "coowner")
    await join(db_session, ws, co_owner, await role_named(db_session, ws, "Owner"))
    async with acting_client(db_session) as client:
        created = await client.act_as(co_owner).post(
            _link(ws, w["notebook"].id), json={}
        )
        assert created.status_code == 200, created.text
        token = created.json()["share_token"]
        assert (await client.act_as(None).get(_public(token))).status_code == 200
        await db_session.execute(
            delete(WorkspaceMembership).where(
                WorkspaceMembership.user_id == co_owner.id,
                WorkspaceMembership.workspace_id == ws,
            )
        )
        await db_session.flush()
        assert (await client.get(_public(token))).status_code == 404


async def test_things_the_chat_made_open_and_unfinished_ones_do_not(
    db_session, world, make_studio_output
):
    w = await world()
    # Made in chat: an artifact in a "notebook" of its own, not a Studio output.
    chat_made = await make_studio_output(
        w["other"],
        kind="report",
        artifact_format="pdf",
        files=(("chart.pdf", "application/pdf", PDF),),
        title="Chart from chat",
    )
    pending = await make_studio_output(
        w["other"],
        kind="video",
        artifact_format="video",
        status=DeliverableJobStatus.RUNNING,
        title="Pending video",
    )
    await _say(
        db_session,
        w["thread"],
        w["owner"].user,
        "Make a chart",
        [
            {
                "type": "tool-call",
                "toolName": "save_artifact",
                "toolCallId": "s1",
                "args": {"title": "Chart"},
                "result": {
                    "status": "saved",
                    "artifact_id": chat_made.artifact.id,
                    "storage_key": "artifacts/secret",
                },
            },
            {
                "type": "tool-call",
                "toolName": "enqueue_deliverable_job",
                "toolCallId": "s2",
                "args": {},
                "result": {"job_id": pending.job.id, "status": "pending"},
            },
            {"type": "text", "text": "Here it is."},
        ],
    )
    async with acting_client(db_session) as client:
        token = (await _share(client, w))["share_token"]
        client.act_as(None)
        body = (await client.get(_public(token))).json()
        made = body["messages"][-1]["content"]
        assert made[0] == {
            "type": "tool-call",
            "toolName": "notebook_artifact",
            "toolCallId": made[0]["toolCallId"],
            "args": {},
            "result": {
                "artifact_id": chat_made.artifact.id,
                "title": "Chart from chat",
                "format": "pdf",
            },
        }
        assert made[1] == {"type": "text", "text": "Here it is."}
        assert "artifacts/secret" not in str(body)
        opened = await client.get(
            _public(token, f"/artifacts/{chat_made.artifact.id}/manifest")
        )
        assert opened.status_code == 200
        assert (
            await client.get(
                _public(token, f"/artifacts/{pending.artifact.id}/manifest")
            )
        ).status_code == 404


async def test_public_routes_are_read_only_rate_limited_and_uncredentialed(
    db_session, world
):
    from app.app import app, limiter

    w = await world()
    async with acting_client(db_session) as client:
        token = (await _share(client, w))["share_token"]
        client.act_as(None)
        for method in ("post", "put", "patch", "delete"):
            response = await getattr(client, method)(_public(token))
            assert response.status_code == 405, method
        page = await client.get(_public(token), headers={"Origin": "https://evil.test"})
        assert page.headers["access-control-allow-origin"] == "*"
        assert "access-control-allow-credentials" not in page.headers
        assert page.headers["referrer-policy"] == "no-referrer"
        preflight = await client.options(
            _public(token),
            headers={
                "Origin": "https://evil.test",
                "Access-Control-Request-Method": "GET",
            },
        )
        assert preflight.status_code == 204
        assert preflight.headers["access-control-allow-origin"] == "*"
        assert "access-control-allow-credentials" not in preflight.headers

    public = {
        route.endpoint.__module__ + "." + route.endpoint.__name__
        for route in app.routes
        if getattr(route, "path", "").startswith("/api/v1/public-notebooks/")
    }
    assert len(public) == 6
    for name in public:
        assert limiter._route_limits.get(name), name


async def test_the_public_links_page_lists_live_links(db_session, world):
    w = await world()
    ws = w["ws"]
    viewer = await new_user(db_session, "viewer")
    await join(db_session, ws, viewer, await role_named(db_session, ws, "Viewer"))
    async with acting_client(db_session) as client:
        created = await _share(client, w, expires_in_days=30)
        listing = f"/api/v1/workspaces/{ws}/notebook-links"
        [row] = (await client.act_as(w["owner"].user).get(listing)).json()["links"]
        assert row["notebook_id"] == w["notebook"].id
        assert row["notebook_title"] == "Ward 4 asthma"
        assert row["share_token"] == created["share_token"]
        assert row["can_revoke"] is True and row["is_expired"] is False
        [seen] = (await client.act_as(viewer).get(listing)).json()["links"]
        assert seen["can_revoke"] is False
        stranger = w["stranger"].user
        assert (await client.act_as(stranger).get(listing)).status_code == 403
        assert (
            await client.act_as(stranger).get(
                f"/api/v1/workspaces/{w['stranger'].workspace.id}/notebook-links"
            )
        ).json()["links"] == []


async def test_viewing_writes_nothing(db_session, world, make_studio_output):
    w = await world()
    quiz = await make_studio_output(
        w["notebook"],
        kind="quiz",
        artifact_format="quiz",
        files=(("quiz.json", "application/json", b'{"questions": []}'),),
        metadata={"keep": "me"},
        title="Asthma quiz",
    )
    async with acting_client(db_session) as client:
        token = (await _share(client, w))["share_token"]
        client.act_as(None)
        await client.get(_public(token))
        await client.get(_public(token, f"/artifacts/{quiz.artifact.id}/manifest"))
        writes = await client.put(
            f"/api/v1/workspaces/{w['ws']}/artifacts/{quiz.artifact.id}/quiz-answer",
            json={"generation": 1, "question_index": 0, "selected_option_index": 0},
        )
        assert writes.status_code == 401
    await db_session.refresh(quiz.artifact)
    assert quiz.artifact.artifact_metadata == {"keep": "me"}


def test_tokens_are_long_and_random():
    from app.public_sharing.notebook_links.service import new_token

    tokens = {new_token() for _ in range(50)}
    assert len(tokens) == 50
    assert all(len(token) >= 64 for token in tokens)
