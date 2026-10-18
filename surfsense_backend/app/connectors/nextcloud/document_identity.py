"""How an imported Nextcloud file is recognised when it is picked again.

The identity is the account and the path, not the connector row: removing and
re-adding the connection must not duplicate what was already imported. The
etag says whether the file changed since.
"""

from __future__ import annotations

from typing import Any
from urllib.parse import urlsplit

from app.db import DocumentType
from app.indexing_pipeline.document_hashing import compute_identifier_hash

from .client import NextcloudAccount

#: Imported files are ordinary uploaded files once they are in the workspace.
DOCUMENT_TYPE = DocumentType.FILE
#: Key of the Nextcloud facts inside ``Document.document_metadata``.
METADATA_KEY = "nextcloud"


def source_id(account: NextcloudAccount, path: str) -> str:
    host = urlsplit(account.base_url).netloc.lower()
    return f"nextcloud://{account.username}@{host}{path}"


def identity_hash(account: NextcloudAccount, path: str, workspace_id: int) -> str:
    return compute_identifier_hash(
        DOCUMENT_TYPE.value, source_id(account, path), workspace_id
    )


def nextcloud_facts(document_metadata: dict[str, Any] | None) -> dict[str, Any]:
    """The ``nextcloud`` block of a document's metadata; empty when it has none."""
    facts = (document_metadata or {}).get(METADATA_KEY)
    return facts if isinstance(facts, dict) else {}
