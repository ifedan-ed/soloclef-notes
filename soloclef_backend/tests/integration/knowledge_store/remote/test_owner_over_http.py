"""The Owner connects a repository over the API and workspace content lands in it.

End to end on real Postgres and real git: the route, the Owner gate, the
connect (clone of the destination), a document written to the workspace, and
the worker's push into the destination repository. Only GitLab's host rule
is skipped (the destination is a local repository).
"""

from __future__ import annotations

import pytest

from app.knowledge_store import KnowledgeStore
from app.knowledge_store.identities import user_identity
from tests.utils.acting_client import acting_client
from tests.utils.workspace_team import join, new_user, role_named

pytestmark = pytest.mark.integration


async def test_owner_connects_and_a_document_is_pushed(
    knowledge_root,
    db_session,
    db_user,
    db_workspace,
    dest,
    local_gitlab,
    delayed,
    workspace_flip,
    celery_session_on_test_connection,
    monkeypatch,
):
    workspace_flip(True)
    editor = await new_user(db_session, "editor")
    await join(
        db_session,
        db_workspace.id,
        editor,
        await role_named(db_session, db_workspace.id, "Editor"),
    )
    url = f"/api/v1/workspaces/{db_workspace.id}/git-remotes"
    body = {
        "provider": "gitlab",
        "url": "https://gitlab.com/ward4/notes",
        "token": "glpat-secret",
        "branch": "main",
        "sourcepath": "notes",
    }
    async with acting_client(db_session) as client:
        refused = await client.act_as(editor).post(url, json=body)
        assert refused.status_code == 403

        # The request names gitlab.com; git is pointed at a local repository.
        import app.knowledge_store.engines.git as git_engine

        monkeypatch.setattr(
            git_engine, "strip_credentials_in_url", lambda url: str(dest._path)
        )
        connected = await client.act_as(db_user).post(url, json=body)
        assert connected.status_code == 200, connected.text
        assert "glpat" not in connected.text
        listed = (await client.get(url)).json()
        assert listed[0]["provider"] == "gitlab"

    store = KnowledgeStore.for_workspace(db_workspace.id).with_session(db_session)
    status = (await store.remotes.list())[0]
    async with store.transaction(message="note", author=user_identity("1")) as tx:
        tx.write(f"{status_mount(status)}/plan.md", b"# Asthma plan\n")
    assert tx.revision is not None
    await store.remotes.sync()

    from dulwich.repo import Repo

    repo = Repo(str(dest._path))
    try:
        sha = repo.refs[b"refs/heads/main"].decode()
    finally:
        repo.close()
    pushed = [p.path for p in dest.list_paths(sha)]
    # The first document of a workspace that was empty when it connected
    # survives the first sync and reaches the repository.
    assert "notes/plan.md" in pushed, pushed
    head = await store.head()
    assert await store.read_as_of(head, f"{status_mount(status)}/plan.md")
    assert dest.read_as_of(sha, "notes/plan.md") == b"# Asthma plan\n"


def status_mount(status) -> str:
    from app.knowledge_store.remote.paths import full_name_from_url, mount

    return mount(
        provider=status.provider,
        full_name=full_name_from_url(status.url),
        sourcepath=status.sourcepath,
    )
