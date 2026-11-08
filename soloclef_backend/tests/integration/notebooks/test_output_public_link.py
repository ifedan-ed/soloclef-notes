"""A finished Studio output shared through a public link, on real Postgres:
Owners only, a frozen copy of its text and one file, and the same expiry."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

import pytest

from app.db import DeliverableJobStatus, PublicChatSnapshot
from app.file_storage import factory
from tests.utils.acting_client import acting_client
from tests.utils.workspace_team import join, new_user, role_named

pytestmark = pytest.mark.integration

PDF = b"%PDF-1.4 shared report"


def _link(ws, nb, output_id):
    return (
        f"/api/v1/workspaces/{ws}/notebooks/{nb}/studio/outputs/{output_id}/public-link"
    )


async def _setup(db_session, make_owner, make_notebook, make_studio_output, **kw):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    made = await make_studio_output(
        notebook,
        kind="report_pdf",
        artifact_format="pdf",
        files=(("report.pdf", "application/pdf", PDF),),
        title="Discharge teaching",
        **kw,
    )
    return owner, notebook, made


async def test_owner_shares_an_output_and_anyone_reads_it(
    db_session,
    make_owner,
    make_notebook,
    make_studio_output,
    artifact_store,
    monkeypatch,
):
    monkeypatch.setattr(factory, "get_storage_backend", lambda *_: artifact_store)
    owner, notebook, made = await _setup(
        db_session, make_owner, make_notebook, make_studio_output
    )
    async with acting_client(db_session) as client:
        created = await client.act_as(owner.user).post(
            _link(owner.workspace.id, notebook.id, made.output.id),
            json={"expires_in_days": 30},
        )
        assert created.status_code == 200, created.text
        token = created.json()["share_token"]

        page = (await client.act_as(None).get(f"/api/v1/public/{token}")).json()
        assert page["kind"] == "output"
        assert page["output"]["title"] == "Discharge teaching"
        assert page["output"]["markdown"].startswith("# ")
        assert page["output"]["file"]["display"] == "pdf"
        assert str(owner.user.id) not in str(page)
        assert "storage_key" not in str(page)

        artifact_id = page["output"]["artifact_id"]
        file = await client.get(
            f"/api/v1/public/{token}/artifacts/{artifact_id}/content"
        )
        assert file.status_code == 200
        assert file.content == PDF
        assert "content-disposition" not in file.headers

        listing = await client.act_as(owner.user).get(
            f"/api/v1/workspaces/{owner.workspace.id}/snapshots"
        )
        [row] = listing.json()["snapshots"]
        assert row["kind"] == "output" and row["notebook_id"] == notebook.id


async def test_editors_and_outsiders_cannot_share_outputs(
    db_session, make_owner, make_notebook, make_studio_output
):
    owner, notebook, made = await _setup(
        db_session, make_owner, make_notebook, make_studio_output
    )
    editor = await new_user(db_session, "editor")
    await join(
        db_session,
        owner.workspace.id,
        editor,
        await role_named(db_session, owner.workspace.id, "Editor"),
    )
    stranger = await make_owner()
    url = _link(owner.workspace.id, notebook.id, made.output.id)
    async with acting_client(db_session) as client:
        assert (await client.act_as(editor).post(url, json={})).status_code == 403
        assert (
            await client.act_as(stranger.user).post(url, json={})
        ).status_code == 403


async def test_unfinished_outputs_cannot_be_shared_and_expired_links_are_gone(
    db_session, make_owner, make_notebook, make_studio_output
):
    owner, notebook, made = await _setup(
        db_session, make_owner, make_notebook, make_studio_output
    )
    running = await make_studio_output(
        notebook,
        kind="quiz",
        artifact_format="quiz",
        status=DeliverableJobStatus.RUNNING,
    )
    async with acting_client(db_session) as client:
        client.act_as(owner.user)
        unfinished = await client.post(
            _link(owner.workspace.id, notebook.id, running.output.id), json={}
        )
        assert unfinished.status_code == 400

        created = (
            await client.post(
                _link(owner.workspace.id, notebook.id, made.output.id), json={}
            )
        ).json()
        snapshot = await db_session.get(PublicChatSnapshot, created["snapshot_id"])
        snapshot.expires_at = datetime.now(UTC) - timedelta(seconds=1)
        await db_session.flush()
        gone = await client.act_as(None).get(f"/api/v1/public/{created['share_token']}")
        assert gone.status_code == 410
