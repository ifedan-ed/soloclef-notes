"""Map SurfSense document types to OKF concept ``type`` strings and ``resource`` URIs.

OKF (Open Knowledge Format v0.1) requires a non-empty, human-friendly ``type`` on
every concept and recommends a ``resource`` URI pointing at the underlying asset.
SurfSense stores the type as a :class:`~app.db.DocumentType` enum and the source
URL (when one exists) inside the free-form ``document_metadata`` JSON, under keys
that differ per connector. This module is the single place that knows both mappings.

Spec: https://github.com/GoogleCloudPlatform/knowledge-catalog/blob/main/okf/SPEC.md
"""

from __future__ import annotations

from typing import Any

from app.db import DocumentType

# Title-cased type strings per Google's sample bundles. Types absent here fall
# back to a derived string (see ``okf_type``), so this need not be exhaustive.
OKF_TYPE_BY_DOCUMENT_TYPE: dict[DocumentType, str] = {
    DocumentType.EXTENSION: "Web Page",
    DocumentType.CRAWLED_URL: "Web Page",
    DocumentType.FILE: "File",
    DocumentType.NOTE: "Note",
    DocumentType.YOUTUBE_VIDEO: "YouTube Video",
    DocumentType.GOOGLE_DRIVE_FILE: "Google Drive File",
    DocumentType.LOCAL_FOLDER_FILE: "File",
}


def _coerce_document_type(
    document_type: DocumentType | str | None,
) -> DocumentType | None:
    if document_type is None:
        return None
    try:
        return DocumentType(document_type)
    except ValueError:
        return None


def okf_type(document_type: DocumentType | str | None) -> str:
    """Return the OKF ``type`` string for a SurfSense document type.

    Falls back to a Title-Cased version of the enum value so the required
    ``type`` field is never empty, even for types added after this map.
    """
    dt = _coerce_document_type(document_type)
    if dt is None:
        if document_type is None:
            return "Document"
        return str(document_type).replace("_", " ").title() or "Document"
    return OKF_TYPE_BY_DOCUMENT_TYPE.get(dt) or dt.value.replace("_", " ").title()


# Per-type metadata keys that hold the canonical source URL, checked first.
_RESOURCE_KEYS_BY_TYPE: dict[DocumentType, tuple[str, ...]] = {
    DocumentType.GOOGLE_DRIVE_FILE: ("webViewLink", "web_view_link"),
    DocumentType.YOUTUBE_VIDEO: ("url", "video_url"),
}

# Generic URL-bearing keys checked for any type as a fallback.
_GENERIC_RESOURCE_KEYS: tuple[str, ...] = (
    "url",
    "URL",
    "source_url",
    "sourceUrl",
    "source",
    "link",
    "permalink",
    "web_url",
    "webViewLink",
    "html_url",
)


def okf_resource(
    document_type: DocumentType | str | None, metadata: dict[str, Any] | None
) -> str | None:
    """Return the canonical source URI for a document, or ``None`` if it has none."""
    if not isinstance(metadata, dict) or not metadata:
        return None

    dt = _coerce_document_type(document_type)
    type_keys = _RESOURCE_KEYS_BY_TYPE.get(dt, ()) if dt is not None else ()

    for key in (*type_keys, *_GENERIC_RESOURCE_KEYS):
        value = metadata.get(key)
        if isinstance(value, str):
            candidate = value.strip()
            if candidate.startswith(("http://", "https://")):
                return candidate
    return None
