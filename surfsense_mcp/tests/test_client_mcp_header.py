"""The client's own-traffic marker header must be a legal HTTP header name.

A header literally named "X-Soloclef Notes-Client" (a rebrand find/replace
that kept the space from the product name) is not a legal header name, so
httpx raised constructing the client and every MCP tool call failed before
reaching the network. This pins the header to a valid name the backend's
PAT-usage analytics middleware actually checks for (app/app.py).
"""

from __future__ import annotations

import asyncio

import httpx

from mcp_server.core.client import SurfSenseClient


def test_client_constructs_without_raising():
    # Regression: constructing the client used to raise
    # `httpx.InvalidURL`/`LocalProtocolError` on the illegal header name.
    SurfSenseClient(api_base="http://test/api/v1", timeout=5, fallback_api_key="x")


def test_mcp_client_marker_header_is_sent_and_legal():
    client = SurfSenseClient(
        api_base="http://test/api/v1", timeout=5, fallback_api_key="ss_pat_x"
    )
    seen: dict = {}

    async def handler(request: httpx.Request) -> httpx.Response:
        seen["header"] = request.headers.get("x-surfsense-client")
        return httpx.Response(200, json={"ok": True})

    # Swap only the transport, so the client's own default headers (set at
    # construction, including the marker under test) are still sent.
    client._http._transport = httpx.MockTransport(handler)
    asyncio.run(client.request("GET", "/documents"))
    assert seen["header"] == "mcp"
