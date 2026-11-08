"""Request and response shapes of the Nextcloud picker API."""

from __future__ import annotations

from datetime import datetime
from typing import Literal

from pydantic import BaseModel, Field

from .webdav_listing import RemoteEntry


class RemoteEntryRead(BaseModel):
    path: str
    name: str
    is_folder: bool
    size: int | None
    modified: datetime | None
    content_type: str | None
    etag: str | None

    @classmethod
    def of(cls, entry: RemoteEntry) -> RemoteEntryRead:
        return cls(
            path=entry.path,
            name=entry.name,
            is_folder=entry.is_folder,
            size=entry.size,
            modified=entry.modified,
            content_type=entry.content_type,
            etag=entry.etag,
        )


class FolderListing(BaseModel):
    path: str
    entries: list[RemoteEntryRead]


class SearchResults(BaseModel):
    query: str
    entries: list[RemoteEntryRead]


class PickedItemIn(BaseModel):
    path: str = Field(min_length=1, max_length=4096)
    kind: Literal["file", "folder"]


class ImportRequest(BaseModel):
    #: Which workspace's knowledge base the picked files land in. The
    #: connector itself is account-level (no single workspace anymore,
    #: ``custom/ACCOUNT_CONNECTIONS.md``), so the caller says where.
    workspace_id: int
    items: list[PickedItemIn] = Field(min_length=1, max_length=500)
    #: "local" | "cloud"; omitted means the server's default parser.
    parser: str | None = None
    use_vision_llm: bool = False
    #: Link the imported files to this notebook as sources.
    notebook_id: int | None = None


class ImportPreview(BaseModel):
    file_count: int
    total_bytes: int
    unsupported_count: int
    too_large_count: int
    over_limit: bool
    max_files: int


class ImportResult(BaseModel):
    document_ids: list[int]
    folder_ids: list[int]
    queued: int
    reused: int
    unsupported: list[str]
    too_large: list[str]


class RefreshResult(BaseModel):
    status: Literal["unchanged", "queued"]


class RefreshableDocument(BaseModel):
    document_id: int
    connector_id: int


class RefreshableDocuments(BaseModel):
    documents: list[RefreshableDocument]
