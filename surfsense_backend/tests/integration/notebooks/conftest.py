"""Fixtures for notebook tests: users with their own workspaces, documents, notebooks."""

from __future__ import annotations

import uuid
from types import SimpleNamespace

import pytest

from app.auth.context import AuthContext
from app.config import config
from app.db import Chunk, Document, DocumentType, Folder, User, Workspace
from app.notebooks.persistence import Notebook, NotebookSource
from app.routes.workspaces_routes import create_default_roles_and_membership

_DIM = config.embedding_model_instance.dimension


def axis(index: int) -> list[float]:
    vector = [0.0] * _DIM
    vector[index] = 1.0
    return vector


@pytest.fixture
def make_owner(db_session):
    """A user plus a workspace they own, as ``POST /workspaces`` would create."""

    async def build() -> SimpleNamespace:
        user = User(
            id=uuid.uuid4(),
            email=f"nb-{uuid.uuid4().hex[:8]}@surfsense.test",
            hashed_password="x",
            is_active=True,
            is_superuser=False,
            is_verified=True,
        )
        db_session.add(user)
        await db_session.flush()
        workspace = Workspace(name=f"Space {uuid.uuid4().hex[:6]}", user_id=user.id)
        db_session.add(workspace)
        await db_session.flush()
        await create_default_roles_and_membership(db_session, workspace.id, user.id)
        await db_session.flush()
        return SimpleNamespace(
            user=user, workspace=workspace, auth=AuthContext.session(user)
        )

    return build


@pytest.fixture
def make_document(db_session):
    async def build(
        workspace_id: int,
        title: str = "Doc",
        text: str = "Some text.",
        folder_id: int | None = None,
        embedding_axis: int = 0,
    ) -> Document:
        document = Document(
            title=title,
            document_type=DocumentType.FILE,
            content=text,
            source_markdown=text,
            content_hash=uuid.uuid4().hex,
            workspace_id=workspace_id,
            folder_id=folder_id,
            status={"state": "ready"},
        )
        db_session.add(document)
        await db_session.flush()
        db_session.add(
            Chunk(
                content=text,
                document_id=document.id,
                position=0,
                embedding=axis(embedding_axis),
            )
        )
        await db_session.flush()
        return document

    return build


@pytest.fixture
def make_folder(db_session):
    async def build(workspace_id: int, name: str = "Folder") -> Folder:
        folder = Folder(name=name, position="0", workspace_id=workspace_id)
        db_session.add(folder)
        await db_session.flush()
        return folder

    return build


@pytest.fixture
def make_notebook(db_session):
    async def build(workspace_id: int, title: str = "Notebook", sources=()) -> Notebook:
        notebook = Notebook(workspace_id=workspace_id, title=title)
        db_session.add(notebook)
        await db_session.flush()
        rows = []
        for source in sources:
            kind, target, *rest = source
            enabled = rest[0] if rest else True
            row = NotebookSource(
                notebook_id=notebook.id,
                document_id=target if kind == "document" else None,
                folder_id=target if kind == "folder" else None,
                enabled=enabled,
            )
            db_session.add(row)
            rows.append(row)
        await db_session.flush()
        # Plain attribute, so tests need no lazy load of the relationship.
        notebook.source_rows = rows
        return notebook

    return build


@pytest.fixture
def artifact_store(tmp_path, monkeypatch):
    """Artifact blobs on a throwaway local disk, where the app reads them."""
    from app.artifacts import storage as artifact_storage
    from app.file_storage.backends.local import LocalFileBackend

    backend = LocalFileBackend(str(tmp_path / "artifacts"))
    monkeypatch.setattr(artifact_storage, "get_storage_backend", lambda *_: backend)
    return backend


@pytest.fixture
def make_studio_output(db_session, artifact_store):
    """A finished studio output: its artifact, its files, and the job that made it."""
    import hashlib

    from app.artifacts.persistence import Artifact, ArtifactFile, ArtifactFileRole
    from app.db import DeliverableJob, DeliverableJobStatus
    from app.notebooks.persistence import NotebookOutput

    async def build(
        notebook: Notebook,
        *,
        kind: str,
        artifact_format: str,
        files: tuple[tuple[str, str, bytes], ...] = (),
        metadata: dict | None = None,
        status: DeliverableJobStatus = DeliverableJobStatus.READY,
        title: str | None = None,
    ) -> SimpleNamespace:
        title = title or f"{kind} output"
        document = Document(
            title=title,
            document_type=DocumentType.ARTIFACT,
            path=f"/artifacts/{uuid.uuid4().hex}.md",
            content=f"# {title}",
            source_markdown=f"# {title}",
            content_hash=uuid.uuid4().hex,
            workspace_id=notebook.workspace_id,
            status={"state": "ready"},
        )
        db_session.add(document)
        await db_session.flush()
        artifact = Artifact(
            document_id=document.id,
            workspace_id=notebook.workspace_id,
            format=artifact_format,
            artifact_metadata=metadata,
        )
        db_session.add(artifact)
        await db_session.flush()
        records = []
        for index, (filename, mime_type, data) in enumerate(files):
            key = f"artifacts/{notebook.workspace_id}/{artifact.id}/{uuid.uuid4().hex}"
            await artifact_store.put(key, data)
            record = ArtifactFile(
                artifact_id=artifact.id,
                role=ArtifactFileRole.PRIMARY
                if index == 0
                else ArtifactFileRole.PREVIEW,
                storage_backend="local",
                storage_key=key,
                original_filename=filename,
                mime_type=mime_type,
                size_bytes=len(data),
                checksum_sha256=hashlib.sha256(data).hexdigest(),
            )
            db_session.add(record)
            records.append(record)
        job = DeliverableJob(
            kind="artifact",
            title=title,
            workspace_id=notebook.workspace_id,
            tool_call_id=f"test:{uuid.uuid4().hex}",
            request={},
            status=status,
            artifact_id=artifact.id if status == DeliverableJobStatus.READY else None,
        )
        db_session.add(job)
        await db_session.flush()
        output = NotebookOutput(
            notebook_id=notebook.id,
            workspace_id=notebook.workspace_id,
            kind=kind,
            title=title,
            options={},
            source_document_ids=[],
            deliverable_job_id=job.id,
        )
        db_session.add(output)
        await db_session.flush()
        return SimpleNamespace(output=output, artifact=artifact, files=records, job=job)

    return build
