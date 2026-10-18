"""Write-only MCP credentials, the stdio refusal and the always-ask tools."""

from __future__ import annotations

import pytest
from pydantic import ValidationError

from app.mcp_security.secrets import (
    MASK,
    MaskedSecretError,
    mask_server_config,
    restore_masked,
)
from app.mcp_security.tool_trust import always_asks, effective_trusted
from app.schemas import MCPServerConfig

pytestmark = pytest.mark.unit

STORED = {
    "url": "https://mcp.tavily.com/mcp/?tavilyApiKey=tvly-SECRETSECRET1234&x=1",
    "headers": {"Authorization": "Bearer bb_live_SECRETSECRET9876"},
    "transport": "streamable-http",
}


def test_mask_hides_all_but_last_four():
    masked = mask_server_config(STORED)
    assert masked["headers"]["Authorization"] == MASK + "9876"
    assert "SECRETSECRET" not in str(masked)
    assert "x=1" in masked["url"]
    assert "1234" in masked["url"]


def test_short_values_are_fully_masked():
    assert mask_server_config({"headers": {"x-api-key": "abc"}})["headers"] == {
        "x-api-key": MASK
    }


def test_round_trip_keeps_the_stored_secret():
    masked = mask_server_config(STORED)
    restored = restore_masked(masked, STORED)
    assert restored["headers"]["Authorization"] == STORED["headers"]["Authorization"]
    assert "tavilyApiKey=tvly-SECRETSECRET1234" in restored["url"]


def test_new_value_replaces_the_stored_one():
    restored = restore_masked(
        {**STORED, "headers": {"Authorization": "Bearer new"}}, STORED
    )
    assert restored["headers"]["Authorization"] == "Bearer new"


def test_masked_value_cannot_follow_a_host_change():
    masked = mask_server_config(STORED)
    masked["url"] = "https://mcp.exa.ai/mcp"
    with pytest.raises(MaskedSecretError):
        restore_masked(masked, STORED)


def test_masked_value_without_a_stored_one_is_refused():
    with pytest.raises(MaskedSecretError):
        restore_masked({"url": "https://mcp.exa.ai/mcp", "headers": {"k": MASK}}, None)


@pytest.mark.parametrize(
    "config",
    [
        {"command": "python", "args": ["-c", "print(1)"]},
        {"command": "sh", "transport": "stdio"},
        {"url": "https://mcp.exa.ai/mcp", "transport": "stdio"},
        {"url": "https://mcp.exa.ai/mcp", "env": {"A": "B"}},
        {"url": "https://mcp.exa.ai/mcp", "args": ["x"]},
        {"url": "https://mcp.exa.ai/mcp", "command": "node"},
        {"url": "https://mcp.exa.ai/mcp", "unexpected": 1},
    ],
)
def test_stdio_configs_are_refused_by_the_schema(config):
    with pytest.raises(ValidationError):
        MCPServerConfig.model_validate(config)


def test_http_config_with_legacy_empty_stdio_fields_is_accepted():
    cfg = MCPServerConfig.model_validate(
        {
            "url": "https://mcp.exa.ai/mcp",
            "command": None,
            "args": [],
            "env": {},
            "transport": "streamable-http",
        }
    )
    assert cfg.model_dump() == {
        "url": "https://mcp.exa.ai/mcp",
        "headers": {},
        "transport": "streamable-http",
    }


@pytest.mark.parametrize(
    "name",
    [
        "navigate",
        "web_fetch_exa",
        "tavily_extract",
        "tavily_crawl",
        "tavily_map",
        "fetch",
    ],
)
def test_url_opening_tools_always_ask(name):
    assert always_asks(name)


def test_trusted_list_drops_url_opening_tools():
    live_browserbase = ["start", "navigate", "extract", "observe", "end"]
    assert effective_trusted(live_browserbase) == ["start", "extract", "observe", "end"]
    assert effective_trusted(["web_search_exa", "web_fetch_exa"]) == ["web_search_exa"]
    assert effective_trusted(["tavily_search", "tavily_extract"]) == ["tavily_search"]
