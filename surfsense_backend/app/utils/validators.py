"""
Validation utilities for SurfSense backend.

This module contains validation functions that were previously scattered across
different modules. It leverages the pyvalidators library where applicable
to avoid rewriting common validation logic.
"""

import re
from typing import Any

import validators
from fastapi import HTTPException

# Connector types that can no longer be connected; existing rows keep working
# until the user removes them.
DEPRECATED_CONNECTOR_TYPES: frozenset[str] = frozenset(
    {
        "WEBCRAWLER_CONNECTOR",
    }
)


def raise_if_connector_deprecated(connector_type: str | Any) -> None:
    """Refuse new connections for a deprecated connector type (HTTP 410 Gone)."""
    connector_type_str = (
        connector_type.value
        if hasattr(connector_type, "value")
        else str(connector_type)
    )
    if connector_type_str in DEPRECATED_CONNECTOR_TYPES:
        pretty = connector_type_str.replace("_CONNECTOR", "").replace("_", " ").title()
        raise HTTPException(
            status_code=410,
            detail=(
                f"The {pretty} connector has been deprecated and can no longer be "
                "connected. If you were relying on it heavily, let us know and we'll "
                "consider bringing it back."
            ),
        )


def validate_workspace_id(workspace_id: Any) -> int:
    """
    Validate and convert workspace_id to integer.

    Args:
        workspace_id: The workspace ID to validate

    Returns:
        int: Validated workspace ID

    Raises:
        HTTPException: If validation fails
    """
    if workspace_id is None:
        raise HTTPException(status_code=400, detail="workspace_id is required")

    if isinstance(workspace_id, bool):
        raise HTTPException(
            status_code=400, detail="workspace_id must be an integer, not a boolean"
        )

    if isinstance(workspace_id, int):
        if workspace_id <= 0:
            raise HTTPException(
                status_code=400, detail="workspace_id must be a positive integer"
            )
        return workspace_id

    if isinstance(workspace_id, str):
        # Check if it's a valid integer string
        if not workspace_id.strip():
            raise HTTPException(status_code=400, detail="workspace_id cannot be empty")

        # Check for valid integer format (no leading zeros, no decimal points)
        if not re.match(r"^[1-9]\d*$", workspace_id.strip()):
            raise HTTPException(
                status_code=400,
                detail="workspace_id must be a valid positive integer",
            )

        value = int(workspace_id.strip())
        # Regex already guarantees value > 0, but check retained for clarity
        if value <= 0:
            raise HTTPException(
                status_code=400, detail="workspace_id must be a positive integer"
            )
        return value

    raise HTTPException(
        status_code=400,
        detail="workspace_id must be an integer or string representation of an integer",
    )


def validate_document_ids(document_ids: Any) -> list[int]:
    """
    Validate and convert document_ids to list of integers.

    Args:
        document_ids: The document IDs to validate

    Returns:
        List[int]: Validated list of document IDs

    Raises:
        HTTPException: If validation fails
    """
    if document_ids is None:
        return []

    if not isinstance(document_ids, list):
        raise HTTPException(
            status_code=400, detail="document_ids_to_add_in_context must be a list"
        )

    validated_ids = []
    for i, doc_id in enumerate(document_ids):
        if isinstance(doc_id, bool):
            raise HTTPException(
                status_code=400,
                detail=f"document_ids_to_add_in_context[{i}] must be an integer, not a boolean",
            )

        if isinstance(doc_id, int):
            if doc_id <= 0:
                raise HTTPException(
                    status_code=400,
                    detail=f"document_ids_to_add_in_context[{i}] must be a positive integer",
                )
            validated_ids.append(doc_id)
        elif isinstance(doc_id, str):
            if not doc_id.strip():
                raise HTTPException(
                    status_code=400,
                    detail=f"document_ids_to_add_in_context[{i}] cannot be empty",
                )

            if not re.match(r"^[1-9]\d*$", doc_id.strip()):
                raise HTTPException(
                    status_code=400,
                    detail=f"document_ids_to_add_in_context[{i}] must be a valid positive integer",
                )

            value = int(doc_id.strip())
            # Regex already guarantees value > 0
            if value <= 0:
                raise HTTPException(
                    status_code=400,
                    detail=f"document_ids_to_add_in_context[{i}] must be a positive integer",
                )
            validated_ids.append(value)
        else:
            raise HTTPException(
                status_code=400,
                detail=f"document_ids_to_add_in_context[{i}] must be an integer or string representation of an integer",
            )

    return validated_ids


def validate_connectors(connectors: Any) -> list[str]:
    """
    Validate selected_connectors list.

    Args:
        connectors: The connectors to validate

    Returns:
        List[str]: Validated list of connector names

    Raises:
        HTTPException: If validation fails
    """
    if connectors is None:
        return []

    if not isinstance(connectors, list):
        raise HTTPException(
            status_code=400, detail="selected_connectors must be a list"
        )

    validated_connectors = []
    for i, connector in enumerate(connectors):
        if not isinstance(connector, str):
            raise HTTPException(
                status_code=400, detail=f"selected_connectors[{i}] must be a string"
            )

        if not connector.strip():
            raise HTTPException(
                status_code=400, detail=f"selected_connectors[{i}] cannot be empty"
            )

        trimmed = connector.strip()
        if not re.fullmatch(r"[\w\-_]+", trimmed):
            raise HTTPException(
                status_code=400,
                detail=f"selected_connectors[{i}] contains invalid characters",
            )
        validated_connectors.append(trimmed)

    return validated_connectors


def validate_research_mode(research_mode: Any) -> str:
    """
    Validate research_mode parameter.

    Args:
        research_mode: The research mode to validate

    Returns:
        str: Validated research mode

    Raises:
        HTTPException: If validation fails
    """
    if research_mode is None:
        return "QNA"  # Default value

    if not isinstance(research_mode, str):
        raise HTTPException(status_code=400, detail="research_mode must be a string")
    normalized_mode = research_mode.strip().upper()
    if not normalized_mode:
        raise HTTPException(status_code=400, detail="research_mode cannot be empty")

    valid_modes = ["QNA"]
    if normalized_mode not in valid_modes:
        raise HTTPException(
            status_code=400,
            detail=f"research_mode must be one of: {', '.join(valid_modes)}",
        )
    return normalized_mode


def validate_search_mode(search_mode: Any) -> str:
    """
    Validate search_mode parameter.

    Args:
        search_mode: The search mode to validate

    Returns:
        str: Validated search mode

    Raises:
        HTTPException: If validation fails
    """
    if search_mode is None:
        return "CHUNKS"  # Default value

    if not isinstance(search_mode, str):
        raise HTTPException(status_code=400, detail="search_mode must be a string")
    normalized_mode = search_mode.strip().upper()
    if not normalized_mode:
        raise HTTPException(status_code=400, detail="search_mode cannot be empty")

    valid_modes = ["CHUNKS", "DOCUMENTS"]
    if normalized_mode not in valid_modes:
        raise HTTPException(
            status_code=400,
            detail=f"search_mode must be one of: {', '.join(valid_modes)}",
        )
    return normalized_mode


def validate_top_k(top_k: Any) -> int:
    """
    Validate and convert top_k to integer.

    Args:
        top_k: The top_k value to validate

    Returns:
        int: Validated top_k value (defaults to 10 if None)

    Raises:
        HTTPException: If validation fails
    """
    if top_k is None:
        return 10  # Default value

    if isinstance(top_k, bool):
        raise HTTPException(
            status_code=400, detail="top_k must be an integer, not a boolean"
        )

    if isinstance(top_k, int):
        if top_k <= 0:
            raise HTTPException(
                status_code=400, detail="top_k must be a positive integer"
            )
        if top_k > 100:
            raise HTTPException(status_code=400, detail="top_k must not exceed 100")
        return top_k

    if isinstance(top_k, str):
        if not top_k.strip():
            raise HTTPException(status_code=400, detail="top_k cannot be empty")

        if not re.match(r"^[1-9]\d*$", top_k.strip()):
            raise HTTPException(
                status_code=400, detail="top_k must be a valid positive integer"
            )

        value = int(top_k.strip())
        if value <= 0:
            raise HTTPException(
                status_code=400, detail="top_k must be a positive integer"
            )
        if value > 100:
            raise HTTPException(status_code=400, detail="top_k must not exceed 100")
        return value

    raise HTTPException(
        status_code=400,
        detail="top_k must be an integer or string representation of an integer",
    )


def validate_messages(messages: Any) -> list[dict]:
    """
    Validate messages structure.

    Args:
        messages: The messages to validate

    Returns:
        List[dict]: Validated messages

    Raises:
        HTTPException: If validation fails
    """
    if not isinstance(messages, list):
        raise HTTPException(status_code=400, detail="messages must be a list")

    if not messages:
        raise HTTPException(status_code=400, detail="messages cannot be empty")

    validated_messages = []
    for i, message in enumerate(messages):
        if not isinstance(message, dict):
            raise HTTPException(
                status_code=400, detail=f"messages[{i}] must be a dictionary"
            )

        if "role" not in message:
            raise HTTPException(
                status_code=400, detail=f"messages[{i}] must have a 'role' field"
            )

        if "content" not in message:
            raise HTTPException(
                status_code=400, detail=f"messages[{i}] must have a 'content' field"
            )

        role = message["role"]
        if not isinstance(role, str) or role not in ["user", "assistant", "system"]:
            raise HTTPException(
                status_code=400,
                detail=f"messages[{i}].role must be 'user', 'assistant', or 'system'",
            )

        content = message["content"]
        if not isinstance(content, str):
            raise HTTPException(
                status_code=400, detail=f"messages[{i}].content must be a string"
            )

        if not content.strip():
            raise HTTPException(
                status_code=400, detail=f"messages[{i}].content cannot be empty"
            )

        # Trim content
        sanitized_content = content.strip()

        validated_messages.append({"role": role, "content": sanitized_content})

    return validated_messages


def validate_email(email: str) -> str:
    """
    Validate email address using pyvalidators library.

    Args:
        email: The email address to validate

    Returns:
        str: Validated email address

    Raises:
        HTTPException: If validation fails
    """
    if not email or not email.strip():
        raise HTTPException(status_code=400, detail="Email address is required")

    email = email.strip()

    if not validators.email(email):
        raise HTTPException(status_code=400, detail="Invalid email address format")

    return email


def validate_url(url: str) -> str:
    """
    Validate URL using pyvalidators library.

    Args:
        url: The URL to validate

    Returns:
        str: Validated URL

    Raises:
        HTTPException: If validation fails
    """
    if not url or not url.strip():
        raise HTTPException(status_code=400, detail="URL is required")

    url = url.strip()

    if not validators.url(url):
        raise HTTPException(status_code=400, detail="Invalid URL format")

    return url


def validate_uuid(uuid_string: str) -> str:
    """
    Validate UUID using pyvalidators library.

    Args:
        uuid_string: The UUID string to validate

    Returns:
        str: Validated UUID string

    Raises:
        HTTPException: If validation fails
    """
    if not uuid_string or not uuid_string.strip():
        raise HTTPException(status_code=400, detail="UUID is required")

    uuid_string = uuid_string.strip()

    if not validators.uuid(uuid_string):
        raise HTTPException(status_code=400, detail="Invalid UUID format")

    return uuid_string


def validate_connector_config(
    connector_type: str | Any, config: dict[str, Any]
) -> dict[str, Any]:
    """Require a connector's config to be a dictionary of settings.

    Raises:
        ValueError: If ``config`` is not a dictionary
    """
    if not isinstance(config, dict) or isinstance(config, bool):
        raise ValueError("config must be a dictionary of connector settings")

    ct_str = connector_type.value if hasattr(connector_type, "value") else str(connector_type)
    if ct_str == "EMAIL_IMAP_CONNECTOR":
        from app.connectors.email_imap.credentials import assert_accounts_are_encrypted

        assert_accounts_are_encrypted(config.get("accounts"))

    return config
