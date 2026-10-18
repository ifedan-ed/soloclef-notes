"""The Browserbase key is read from its MCP connector's config, server-side."""

from __future__ import annotations

import pytest

from app.notebooks.research.web_apis.keys import key_from_mcp_config

pytestmark = pytest.mark.unit


def _config(url: str, headers: dict | None = None) -> dict:
    return {"server_config": {"url": url, "headers": headers or {}}}


@pytest.mark.parametrize(
    ("config", "expected"),
    [
        (
            _config(
                "https://mcp.browserbase.com/mcp", {"Authorization": "Bearer bb_1"}
            ),
            "bb_1",
        ),
        (_config("https://mcp.browserbase.com/mcp", {"X-BB-API-Key": "bb_2"}), "bb_2"),
        (_config("https://mcp.browserbase.com/mcp?browserbaseApiKey=bb_3"), "bb_3"),
        # Another provider's server never yields a Browserbase key.
        (_config("https://mcp.exa.ai/mcp", {"Authorization": "Bearer exa"}), None),
        (
            _config(
                "https://browserbase.com.evil.test/mcp", {"Authorization": "Bearer x"}
            ),
            None,
        ),
    ],
)
def test_browserbase_key_forms(config, expected):
    assert key_from_mcp_config(config, "browserbase") == expected
