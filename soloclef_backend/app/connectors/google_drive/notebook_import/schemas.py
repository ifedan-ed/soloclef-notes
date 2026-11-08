"""The import request and result. File ids come from the Google Picker."""

from __future__ import annotations

from pydantic import BaseModel, Field

MAX_PICKED_FILES = 50


class PickedDriveFile(BaseModel):
    id: str = Field(min_length=10, max_length=200, pattern=r"^[A-Za-z0-9_-]+$")
    name: str = Field(min_length=1, max_length=512)
    mime_type: str = Field(default="", max_length=255)


class DriveImportRequest(BaseModel):
    #: Which workspace's knowledge base the picks land in. The connector
    #: itself is account-level (no single workspace anymore,
    #: custom/ACCOUNT_CONNECTIONS.md), so the caller says where.
    workspace_id: int
    files: list[PickedDriveFile] = Field(min_length=1, max_length=MAX_PICKED_FILES)
    #: The notebook to add the files to; none imports into the library only.
    notebook_id: int | None = None


class DriveImportResult(BaseModel):
    #: The document each supported pick maps to (new or already imported).
    document_ids: list[int]
    #: Picks queued for download and parsing.
    queued: int
    #: Names of picks no parser here can read (not imported).
    unsupported: list[str]
