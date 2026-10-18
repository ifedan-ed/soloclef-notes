"""Tests for the validators module."""

import pytest
from fastapi import HTTPException

from app.utils.validators import (
    raise_if_connector_deprecated,
    validate_connector_config,
    validate_connectors,
    validate_document_ids,
    validate_email,
    validate_messages,
    validate_research_mode,
    validate_search_mode,
    validate_top_k,
    validate_url,
    validate_uuid,
    validate_workspace_id,
)

pytestmark = pytest.mark.unit


# ---------------------------------------------------------------------------
# IDs and Pagination Validators
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "valid_input, expected",
    [
        (1, 1),
        (42, 42),
        ("1", 1),
        (" 42 ", 42),
    ],
)
def test_validate_workspace_id_valid(valid_input, expected):
    assert validate_workspace_id(valid_input) == expected


@pytest.mark.parametrize(
    "invalid_input",
    [
        None,
        True,
        False,
        0,
        -1,
        "",
        "   ",
        "abc",
        "1.5",
        "0",
        "-5",
    ],
)
def test_validate_workspace_id_invalid(invalid_input):
    with pytest.raises(HTTPException) as excinfo:
        validate_workspace_id(invalid_input)
    assert excinfo.value.status_code == 400


def test_validate_document_ids_valid():
    assert validate_document_ids(None) == []
    assert validate_document_ids([1, 2, 3]) == [1, 2, 3]
    assert validate_document_ids(["1", " 2 ", 3]) == [1, 2, 3]


@pytest.mark.parametrize(
    "invalid_input",
    [
        "not a list",
        123,
        [True],
        [0],
        [-1],
        [""],
        ["   "],
        ["abc"],
        [1, "abc"],
    ],
)
def test_validate_document_ids_invalid(invalid_input):
    with pytest.raises(HTTPException) as excinfo:
        validate_document_ids(invalid_input)
    assert excinfo.value.status_code == 400


def test_validate_top_k_valid():
    assert validate_top_k(None) == 10
    assert validate_top_k(5) == 5
    assert validate_top_k("20") == 20
    assert validate_top_k(100) == 100


@pytest.mark.parametrize(
    "invalid_input",
    [
        True,
        False,
        0,
        -1,
        101,
        "",
        "abc",
        "101",
        "0",
    ],
)
def test_validate_top_k_invalid(invalid_input):
    with pytest.raises(HTTPException) as excinfo:
        validate_top_k(invalid_input)
    assert excinfo.value.status_code == 400


# ---------------------------------------------------------------------------
# Format Validators
# ---------------------------------------------------------------------------


def test_validate_email_valid():
    assert validate_email("test@example.com") == "test@example.com"
    assert validate_email("  user@domain.co.uk  ") == "user@domain.co.uk"


@pytest.mark.parametrize(
    "invalid_input",
    [
        "",
        "   ",
        None,
        "not-an-email",
        "test@.com",
        "@example.com",
    ],
)
def test_validate_email_invalid(invalid_input):
    with pytest.raises(HTTPException) as excinfo:
        validate_email(invalid_input)
    assert excinfo.value.status_code == 400


def test_validate_url_valid():
    assert validate_url("https://example.com") == "https://example.com"
    assert validate_url("  http://test.org:8000  ") == "http://test.org:8000"


@pytest.mark.parametrize(
    "invalid_input",
    [
        "",
        "   ",
        None,
        "not-a-url",
        "htt://invalid",
    ],
)
def test_validate_url_invalid(invalid_input):
    with pytest.raises(HTTPException) as excinfo:
        validate_url(invalid_input)
    assert excinfo.value.status_code == 400


def test_validate_uuid_valid():
    valid_uuid = "123e4567-e89b-12d3-a456-426614174000"
    assert validate_uuid(valid_uuid) == valid_uuid
    assert validate_uuid(f"  {valid_uuid}  ") == valid_uuid


@pytest.mark.parametrize(
    "invalid_input",
    [
        "",
        "   ",
        None,
        "not-a-uuid",
        "123e4567-e89b-12d3-a456",
    ],
)
def test_validate_uuid_invalid(invalid_input):
    with pytest.raises(HTTPException) as excinfo:
        validate_uuid(invalid_input)
    assert excinfo.value.status_code == 400


# ---------------------------------------------------------------------------
# Enum and List Validators
# ---------------------------------------------------------------------------


def test_validate_connectors_valid():
    assert validate_connectors(None) == []
    assert validate_connectors(["GOOGLE_DRIVE_CONNECTOR", "MCP_CONNECTOR"]) == [
        "GOOGLE_DRIVE_CONNECTOR",
        "MCP_CONNECTOR",
    ]
    assert validate_connectors(["  my-connector_123  "]) == ["my-connector_123"]


@pytest.mark.parametrize(
    "invalid_input",
    [
        "not a list",
        [123],
        [True],
        [""],
        ["   "],
        ["invalid connector!"],
        ["connector 1"],
    ],
)
def test_validate_connectors_invalid(invalid_input):
    with pytest.raises(HTTPException) as excinfo:
        validate_connectors(invalid_input)
    assert excinfo.value.status_code == 400


def test_validate_research_mode_valid():
    assert validate_research_mode(None) == "QNA"
    assert validate_research_mode("QNA") == "QNA"
    assert validate_research_mode("  qna  ") == "QNA"


@pytest.mark.parametrize(
    "invalid_input",
    [
        123,
        "",
        "   ",
        "INVALID",
    ],
)
def test_validate_research_mode_invalid(invalid_input):
    with pytest.raises(HTTPException) as excinfo:
        validate_research_mode(invalid_input)
    assert excinfo.value.status_code == 400


def test_validate_search_mode_valid():
    assert validate_search_mode(None) == "CHUNKS"
    assert validate_search_mode("CHUNKS") == "CHUNKS"
    assert validate_search_mode("  documents  ") == "DOCUMENTS"


@pytest.mark.parametrize(
    "invalid_input",
    [
        123,
        "",
        "   ",
        "INVALID",
    ],
)
def test_validate_search_mode_invalid(invalid_input):
    with pytest.raises(HTTPException) as excinfo:
        validate_search_mode(invalid_input)
    assert excinfo.value.status_code == 400


# ---------------------------------------------------------------------------
# Complex Validators
# ---------------------------------------------------------------------------


def test_validate_messages_valid():
    messages = [
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": "Hello!"},
        {"role": "assistant", "content": "Hi there!"},
    ]
    assert validate_messages(messages) == messages

    # Test trimming
    assert validate_messages([{"role": "user", "content": "  trimmed  "}]) == [
        {"role": "user", "content": "trimmed"}
    ]


@pytest.mark.parametrize(
    "invalid_input",
    [
        "not a list",
        [],
        [123],
        [{"role": "user"}],  # Missing content
        [{"content": "hi"}],  # Missing role
        [{"role": "invalid", "content": "hi"}],  # Invalid role
        [{"role": "user", "content": 123}],  # Non-string content
        [{"role": "user", "content": ""}],  # Empty content
        [{"role": "user", "content": "   "}],  # Whitespace-only content
    ],
)
def test_validate_messages_invalid(invalid_input):
    with pytest.raises(HTTPException) as excinfo:
        validate_messages(invalid_input)
    assert excinfo.value.status_code == 400


def test_validate_connector_config_valid():
    assert validate_connector_config("MCP_CONNECTOR", {"any": "value"}) == {
        "any": "value"
    }


def test_validate_connector_config_invalid():
    with pytest.raises(ValueError):
        validate_connector_config("MCP_CONNECTOR", "not a dict")


def test_raise_if_connector_deprecated_blocks():
    """Deprecated connector types are refused with HTTP 410."""
    with pytest.raises(HTTPException) as excinfo:
        raise_if_connector_deprecated("WEBCRAWLER_CONNECTOR")
    assert excinfo.value.status_code == 410


@pytest.mark.parametrize(
    "connector_type",
    ["GOOGLE_DRIVE_CONNECTOR", "MCP_CONNECTOR"],
)
def test_raise_if_connector_deprecated_allows_active(connector_type):
    """Active connector types pass through without raising."""
    raise_if_connector_deprecated(connector_type)
