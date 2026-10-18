"""Academic search tools round-trip through the real tool registration.

Each tool must call the backend's standalone academic-search door at the right
path with the right body, and render a readable result (or pass through raw
JSON on request).
"""

from __future__ import annotations

import asyncio
from unittest.mock import MagicMock

import httpx
from mcp.server.fastmcp import FastMCP

from mcp_server.core.client import SurfSenseClient
from mcp_server.features.academic import tools as academic_tools

_ROW = {
    "id": "pubmed:1",
    "url": "https://pubmed.ncbi.nlm.nih.gov/1/",
    "title": "High-flow nasal cannula in bronchiolitis",
    "snippet": "A randomized trial of HFNC in infants with bronchiolitis.",
    "source": "pubmed",
    "thumbnail_url": None,
    "favicon_url": None,
    "meta": "Smith J, et al. · NEJM · 2020",
    "paper": {
        "doi": "10.1056/nejmoa1000000",
        "pmid": "1",
        "pmcid": "PMC1",
        "arxiv_id": None,
        "server": None,
        "authors": ["Smith J"],
        "venue": "NEJM",
        "year": 2020,
    },
}


def _client_recording(seen: dict, row: dict = _ROW) -> SurfSenseClient:
    async def handler(request: httpx.Request) -> httpx.Response:
        import json as _json

        seen["path"] = request.url.path
        seen["body"] = _json.loads(request.content or b"{}")
        return httpx.Response(200, json=[row])

    client = SurfSenseClient(
        api_base="http://test/api/v1", timeout=5, fallback_api_key="ss_pat_x"
    )
    client._http = httpx.AsyncClient(
        base_url="http://test/api/v1",
        headers={"Accept": "application/json"},
        transport=httpx.MockTransport(handler),
    )
    return client


def _workspace_context():
    workspace = MagicMock()
    workspace.id = 7
    context = MagicMock()

    async def resolve(_reference):
        return workspace

    context.resolve = resolve
    return context


def _call(client: SurfSenseClient, tool: str, **arguments) -> str:
    mcp = FastMCP("test")
    academic_tools.register(mcp, client, _workspace_context())
    blocks = asyncio.run(mcp.call_tool(tool, arguments))
    return "".join(block.text for block in blocks)


def test_pubmed_search_hits_the_right_path_and_body():
    seen: dict = {}
    text = _call(
        _client_recording(seen),
        "soloclef_pubmed_search",
        query="bronchiolitis high flow nasal cannula",
        since=2018,
        full_text=True,
    )
    assert seen["path"] == "/api/v1/workspaces/7/academic-search/pubmed"
    assert seen["body"]["query"] == "bronchiolitis high flow nasal cannula"
    assert seen["body"]["since"] == 2018
    assert seen["body"]["full_text"] is True
    assert "High-flow nasal cannula" in text
    assert "PMID: 1" in text
    assert "https://pubmed.ncbi.nlm.nih.gov/1/" in text


def test_arxiv_search_hits_the_right_path():
    seen: dict = {}
    _call(_client_recording(seen), "soloclef_arxiv_search", query="transformers")
    assert seen["path"] == "/api/v1/workspaces/7/academic-search/arxiv"


def test_preprint_search_sends_the_server():
    seen: dict = {}
    _call(
        _client_recording(seen),
        "soloclef_preprint_search",
        query="asthma",
        server="medrxiv",
    )
    assert seen["path"] == "/api/v1/workspaces/7/academic-search/medrxiv"
    assert seen["body"]["server"] == "medrxiv"


def test_scholar_search_hits_the_right_path():
    seen: dict = {}
    _call(_client_recording(seen), "soloclef_scholar_search", query="sepsis")
    assert seen["path"] == "/api/v1/workspaces/7/academic-search/google_scholar"


def test_json_response_format_passes_rows_through():
    seen: dict = {}
    text = _call(
        _client_recording(seen),
        "soloclef_pubmed_search",
        query="x",
        response_format="json",
    )
    assert '"pmid": "1"' in text


def test_no_results_renders_a_clear_message():
    mcp = FastMCP("test")

    async def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=[])

    client = SurfSenseClient(
        api_base="http://test/api/v1", timeout=5, fallback_api_key="ss_pat_x"
    )
    client._http = httpx.AsyncClient(
        base_url="http://test/api/v1",
        headers={"Accept": "application/json"},
        transport=httpx.MockTransport(handler),
    )
    academic_tools.register(mcp, client, _workspace_context())
    blocks = asyncio.run(
        mcp.call_tool("soloclef_pubmed_search", {"query": "nothing matches this"})
    )
    text = "".join(block.text for block in blocks)
    assert "No results" in text
