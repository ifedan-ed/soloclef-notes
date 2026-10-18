"""Map configured connectors to the searchable document/connector types.

This is agent-agnostic infrastructure shared by every agent factory (single-
and multi-agent). It translates the connectors a workspace has enabled into
the set of searchable type strings that pre-search middleware understands, and
always layers in the document types that exist independently of any connector
(uploads, notes, extension captures, YouTube).

It lives in its own module — rather than inside a specific agent factory — so
that retiring or moving any single agent never disturbs the others' access to
this mapping.
"""

from __future__ import annotations

from typing import Any

# Maps SearchSourceConnectorType enum values to the searchable document/connector types
# used by KB pre-search middleware. All entries are local/indexed data.
_CONNECTOR_TYPE_TO_SEARCHABLE: dict[str, str] = {
    "GOOGLE_DRIVE_CONNECTOR": "GOOGLE_DRIVE_FILE",  # Connector type differs from document type
    # Generic user-defined MCP server: unlocks the mcp_discovery subagent.
    "MCP_CONNECTOR": "MCP_CONNECTOR",
    # Live, read-only: unlocks the email_imap subagent. Never indexed.
    "EMAIL_IMAP_CONNECTOR": "EMAIL_IMAP_CONNECTOR",
}

# Document types that don't come from SearchSourceConnector but should always be searchable
_ALWAYS_AVAILABLE_DOC_TYPES: list[str] = [
    "EXTENSION",  # Browser extension data
    "FILE",  # Uploaded files
    "NOTE",  # User notes
    "YOUTUBE_VIDEO",  # YouTube videos
]


def map_connectors_to_searchable_types(
    connector_types: list[Any],
) -> list[str]:
    """
    Map SearchSourceConnectorType enums to searchable document/connector types.

    This function:
    1. Converts connector type enums to their searchable counterparts
    2. Includes always-available document types (EXTENSION, FILE, NOTE, YOUTUBE_VIDEO)
    3. Deduplicates while preserving order

    Args:
        connector_types: List of SearchSourceConnectorType enum values

    Returns:
        List of searchable connector/document type strings
    """
    result_set: set[str] = set()
    result_list: list[str] = []

    # Add always-available document types first
    for doc_type in _ALWAYS_AVAILABLE_DOC_TYPES:
        if doc_type not in result_set:
            result_set.add(doc_type)
            result_list.append(doc_type)

    # Map each connector type to its searchable equivalent
    for ct in connector_types:
        # Handle both enum and string types
        ct_str = ct.value if hasattr(ct, "value") else str(ct)
        searchable = _CONNECTOR_TYPE_TO_SEARCHABLE.get(ct_str)
        if searchable and searchable not in result_set:
            result_set.add(searchable)
            result_list.append(searchable)

    return result_list
