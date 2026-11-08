"""The phone app's Drive Picker through the browser hand-off, end to end on the stand-in Google.

The app asks for a code with its session; the browser sheet opens the code's
page once; the page carries the Picker's token and a ticket; its import adds
the picks to the notebook exactly as the web's import does; and every way it
can fail goes back to the app's own address.
"""

from __future__ import annotations

import json
import re

import pytest
from sqlalchemy import select
from sqlalchemy.orm.attributes import flag_modified

from app.notebooks.persistence import NotebookSource
from tests.integration.connectors.google_drive.conftest import connect

pytestmark = pytest.mark.integration

NOTES = {
    "id": "e2eDriveNotes0001",
    "name": "Drive field notes.md",
    "mime_type": "text/markdown",
}
PLAN = {
    "id": "e2eDrivePlan00002",
    "name": "Research plan.txt",
    "mime_type": "text/plain",
}
FOLDER = {
    "id": "e2eDriveFolder004",
    "name": "A folder",
    "mime_type": "application/vnd.google-apps.folder",
}


async def _connected(handoff_api, db_session, google, owner):
    await connect(handoff_api(owner.auth), owner, google)
    from app.connectors.google_drive.connect.connection_record import users_connection

    return await users_connection(db_session, owner.user.id, owner.workspace.id)


def _config(page: str) -> dict:
    match = re.search(
        r'<script id="handoff-config" type="application/json">(.*?)</script>',
        page,
        re.S,
    )
    assert match, page
    return json.loads(match.group(1))


async def _start(client, owner, notebook_id, purpose="drive_picker"):
    return await client.post(
        "/api/v1/app/handoff",
        json={
            "purpose": purpose,
            "workspace_id": owner.workspace.id,
            "notebook_id": notebook_id,
        },
    )


async def test_pick_in_the_browser_sheet_and_the_files_join_the_notebook(
    handoff_api, db_session, google, make_owner, make_notebook
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    connector = await _connected(handoff_api, db_session, google, owner)
    client = handoff_api(owner.auth)

    started = await _start(client, owner, notebook.id)
    assert started.status_code == 200, started.text
    body = started.json()
    assert body["expires_in"] == 60
    # The Picker's page names its purpose, so a spent code gets the Picker's own 410 page.
    assert body["path"] == f"/api/v1/app/handoff/{body['code']}?for=drive_picker"
    assert started.headers["cache-control"] == "no-store"

    # The browser sheet has no session: the code alone opens the page.
    sheet = handoff_api(None)
    page = await sheet.get(body["path"])
    assert page.status_code == 200, page.text
    assert page.headers["cache-control"] == "no-store"
    assert "frame-ancestors 'none'" in page.headers["content-security-policy"]
    config = _config(page.text)
    assert (
        config["accessToken"]
        and config["apiKey"] == "it-picker-key"
        and config["appId"] == "123456789012"
    )
    assert config["doneUrl"] == "soloclef://drive/done"
    assert config["importPath"] == "/api/v1/app/handoff/drive-picker/import"

    # Opened once only.
    again = await sheet.get(body["path"])
    assert again.status_code == 410
    assert "run out" in again.text

    imported = await sheet.post(
        config["importPath"],
        json={"ticket": config["ticket"], "files": [NOTES, PLAN, FOLDER]},
    )
    assert imported.status_code == 200, imported.text
    result = imported.json()
    assert result["queued"] == 2
    assert result["unsupported"] == ["A folder"]
    linked = set(
        (
            await db_session.scalars(
                select(NotebookSource.document_id).where(
                    NotebookSource.notebook_id == notebook.id
                )
            )
        ).all()
    )
    assert linked == set(result["document_ids"])
    [(connector_id, workspace_id, user_id, picks)] = handoff_api.dispatched
    assert (connector_id, workspace_id, user_id) == (
        connector.id,
        owner.workspace.id,
        owner.user.id,
    )
    assert [p.id for p in picks] == [NOTES["id"], PLAN["id"]]

    # The ticket makes one import.
    twice = await sheet.post(
        config["importPath"], json={"ticket": config["ticket"], "files": [NOTES]}
    )
    assert twice.status_code == 410
    assert twice.json()["detail"]["code"] == "HANDOFF_EXPIRED"


async def test_a_made_up_code_or_ticket_opens_nothing(handoff_api, make_owner):
    sheet = handoff_api(None)
    page = await sheet.get("/api/v1/app/handoff/" + "x" * 43 + "?for=drive_picker")
    assert page.status_code == 410
    imported = await sheet.post(
        "/api/v1/app/handoff/drive-picker/import",
        json={"ticket": "y" * 43, "files": [NOTES]},
    )
    assert imported.status_code == 410


async def test_a_purpose_the_server_does_not_know_is_refused(
    handoff_api, db_session, google, make_owner
):
    owner = await make_owner()
    await _connected(handoff_api, db_session, google, owner)
    # drive_connect and microsoft_mail are built (B10): tests/unit/app_handoff/test_routes.py.
    for purpose in ("elsewhere", "calendar_connect"):
        response = await _start(handoff_api(owner.auth), owner, None, purpose)
        assert response.status_code == 400
        assert response.json()["detail"]["code"] == "HANDOFF_PURPOSE_UNSUPPORTED"


async def test_without_a_drive_connection_no_code_is_issued(
    handoff_api, google, make_owner, make_notebook
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    response = await _start(handoff_api(owner.auth), owner, notebook.id)
    assert response.status_code == 404
    assert response.json()["detail"]["code"] == "google_drive_not_connected"


async def test_a_notebook_of_another_workspace_is_refused(
    handoff_api, db_session, google, make_owner, make_notebook
):
    owner = await make_owner()
    stranger = await make_owner()
    foreign = await make_notebook(stranger.workspace.id)
    await _connected(handoff_api, db_session, google, owner)
    response = await _start(handoff_api(owner.auth), owner, foreign.id)
    assert response.status_code == 404


async def test_a_connection_google_stopped_accepting_goes_back_to_the_app_to_reconnect(
    handoff_api, db_session, google, make_owner, make_notebook
):
    owner = await make_owner()
    notebook = await make_notebook(owner.workspace.id)
    connector = await _connected(handoff_api, db_session, google, owner)
    started = await _start(handoff_api(owner.auth), owner, notebook.id)
    assert started.status_code == 200, started.text

    connector.config = {**connector.config, "auth_expired": True}
    flag_modified(connector, "config")
    await db_session.flush()

    page = await handoff_api(None).get(started.json()["path"], follow_redirects=False)
    assert page.status_code == 302
    assert (
        page.headers["location"] == "soloclef://drive/done?error=google_drive_reconnect"
    )


async def test_already_needing_a_reconnect_issues_no_code(
    handoff_api, db_session, google, make_owner
):
    owner = await make_owner()
    connector = await _connected(handoff_api, db_session, google, owner)
    connector.config = {**connector.config, "auth_expired": True}
    flag_modified(connector, "config")
    await db_session.flush()
    response = await _start(handoff_api(owner.auth), owner, None)
    assert response.status_code == 400
    assert response.json()["detail"]["code"] == "google_drive_reconnect"
