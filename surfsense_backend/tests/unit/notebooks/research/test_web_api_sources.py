"""Exa and Tavily as research sources: the request each search sends, the rows it
reads back, where the key comes from, and what happens without one.

Only the providers' HTTP APIs are stood in (the ``send`` seam); everything else is real.
"""

from __future__ import annotations

import httpx
import pytest

from app.notebooks.research.schemas import SearchRequest
from app.notebooks.research.search import research_events
from app.notebooks.research.source_errors import SourceUnavailableError
from app.notebooks.research.web_apis import exa, tavily
from app.notebooks.research.web_apis.keys import key_from_connector, key_from_mcp_config
from app.notebooks.research.web_apis.requests import ApiReply
from app.notebooks.research.web_apis.search import WebApiSearch

pytestmark = pytest.mark.unit

EXA_KEY = "11111111-2222-3333-4444-555555555555"
TAVILY_KEY = "tvly-dev-abc123"

EXA_REPLY = {
    "results": [
        {
            "title": "School nurses and asthma",
            "url": "https://www.example.org/asthma",
            "publishedDate": "2024-05-01T00:00:00.000Z",
            "author": "A. Author",
            "highlights": ["Nurses **manage** asthma  at school."],
        },
        {"title": "No url", "url": "ftp://nope"},
        {"title": "", "url": "https://b.example/x", "text": "Fallback text"},
    ]
}
TAVILY_REPLY = {
    "query": "q",
    "results": [
        {
            "title": "Asthma action plans",
            "url": "https://www.cdc.example/asthma",
            "content": "An asthma action plan <b>helps</b>.",
            "score": 0.9,
            "published_date": "2025-02-03",
        },
        {"title": "Second", "url": "https://two.example/", "content": "two"},
    ],
}


class FakeApi:
    def __init__(self, reply=None, error=None):
        self.reply = reply
        self.error = error
        self.sent = []

    async def __call__(self, request):
        self.sent.append(request)
        if self.error:
            raise self.error
        return self.reply


def _search(keys, api):
    async def lookup():
        return keys

    return WebApiSearch(lookup, api)


# --- request building --------------------------------------------------------


def test_exa_fast_request():
    request = exa.build_request("asthma", 10, deep=False, key=EXA_KEY)
    assert request.url == "https://api.exa.ai/search"
    assert request.headers["x-api-key"] == EXA_KEY
    assert request.body["type"] == "fast"
    assert request.body["numResults"] == 10
    assert request.body["contents"] == {
        "highlights": {"maxCharacters": exa.HIGHLIGHT_CHARS, "query": "asthma"}
    }
    # Page text is never asked for: imports read the page themselves.
    assert "text" not in request.body["contents"]


def test_exa_deep_request_uses_auto_and_caps_results():
    request = exa.build_request("asthma", 250, deep=True, key=EXA_KEY)
    assert request.body["type"] == "auto"
    assert request.body["numResults"] == exa.MAX_RESULTS


def test_tavily_fast_and_deep_requests():
    fast = tavily.build_request("asthma", 10, deep=False, key=TAVILY_KEY)
    deep = tavily.build_request("asthma", 25, deep=True, key=TAVILY_KEY)
    assert fast.url == "https://api.tavily.com/search"
    assert fast.headers["Authorization"] == f"Bearer {TAVILY_KEY}"
    assert "api_key" not in fast.body
    assert (fast.body["search_depth"], fast.body["max_results"]) == ("basic", 10)
    assert (deep.body["search_depth"], deep.body["max_results"]) == ("advanced", 20)
    assert fast.body["include_raw_content"] is False
    assert fast.body["include_answer"] is False


# --- parsing -----------------------------------------------------------------


def test_exa_rows():
    rows = exa.to_rows(EXA_REPLY)
    assert [r.url for r in rows] == [
        "https://www.example.org/asthma",
        "https://b.example/x",
    ]
    first = rows[0]
    assert first.source == "exa"
    assert first.title == "School nurses and asthma"
    assert first.snippet == "Nurses manage asthma at school."
    assert first.meta == "example.org · A. Author · 2024-05-01"
    # A missing title falls back to the URL; a missing highlight to any text.
    assert rows[1].title == "https://b.example/x"
    assert rows[1].snippet == "Fallback text"


def test_tavily_rows():
    rows = tavily.to_rows(TAVILY_REPLY)
    assert [r.source for r in rows] == ["tavily", "tavily"]
    assert rows[0].snippet == "An asthma action plan helps ."
    assert rows[0].meta == "cdc.example · 2025-02-03"
    assert rows[1].meta == "two.example"


@pytest.mark.parametrize("junk", [None, [], {"results": None}, {"results": ["x", 3]}])
def test_malformed_replies_give_no_rows(junk):
    assert exa.to_rows(junk) == []
    assert tavily.to_rows(junk) == []


# --- keys --------------------------------------------------------------------


def _mcp(url, headers):
    return {
        "server_config": {
            "url": url,
            "transport": "streamable-http",
            "headers": headers,
        }
    }


def test_exa_key_from_mcp_header():
    config = _mcp("https://mcp.exa.ai/mcp", {"x-api-key": EXA_KEY})
    assert key_from_mcp_config(config, "exa") == EXA_KEY
    assert key_from_mcp_config(config, "tavily") is None


def test_tavily_key_from_mcp_bearer_or_url():
    bearer = _mcp(
        "https://mcp.tavily.com/mcp/", {"Authorization": f"Bearer {TAVILY_KEY}"}
    )
    in_url = _mcp(f"https://mcp.tavily.com/mcp/?tavilyApiKey={TAVILY_KEY}", {})
    assert key_from_mcp_config(bearer, "tavily") == TAVILY_KEY
    assert key_from_mcp_config(in_url, "tavily") == TAVILY_KEY
    assert key_from_mcp_config(bearer, "exa") is None


def test_lookalike_hosts_are_not_trusted_with_a_key():
    config = _mcp("https://exa.ai.evil.example/mcp", {"x-api-key": EXA_KEY})
    assert key_from_mcp_config(config, "exa") is None


def test_only_mcp_connectors_carry_a_key():
    config = _mcp(
        "https://mcp.tavily.com/mcp", {"Authorization": f"Bearer {TAVILY_KEY}"}
    )
    assert key_from_connector("MCP_CONNECTOR", config, "tavily") == TAVILY_KEY
    assert key_from_connector("GOOGLE_DRIVE_CONNECTOR", config, "tavily") is None


# --- searching ---------------------------------------------------------------


async def test_search_sends_one_request_with_the_workspace_key():
    api = FakeApi(ApiReply(200, TAVILY_REPLY))
    rows = await _search({"tavily": TAVILY_KEY}, api).search(
        "tavily", "asthma", 10, deep=False
    )
    assert len(api.sent) == 1
    assert api.sent[0].headers["Authorization"] == f"Bearer {TAVILY_KEY}"
    assert [r.title for r in rows] == ["Asthma action plans", "Second"]


async def test_without_a_key_the_source_is_unavailable_and_nothing_is_sent():
    api = FakeApi(ApiReply(200, EXA_REPLY))
    search = _search({"tavily": TAVILY_KEY}, api)
    assert await search.availability() == {"exa": False, "tavily": True}
    with pytest.raises(SourceUnavailableError) as raised:
        await search.search("exa", "asthma", 10, deep=False)
    assert raised.value.code == "source_not_connected"
    assert api.sent == []


@pytest.mark.parametrize(
    ("status", "code"),
    [
        (401, "source_key_rejected"),
        (429, "source_quota"),
        (432, "source_quota"),
        (402, "source_quota"),
        (503, "source_busy"),
    ],
)
async def test_provider_refusals_become_inline_errors_without_retrying(status, code):
    api = FakeApi(ApiReply(status, {"error": "nope"}))
    with pytest.raises(SourceUnavailableError) as raised:
        await _search({"exa": EXA_KEY}, api).search("exa", "asthma", 10, deep=False)
    assert raised.value.code == code
    assert EXA_KEY not in raised.value.message
    assert len(api.sent) == 1


async def test_unreachable_provider_is_busy_not_retried():
    api = FakeApi(error=httpx.ConnectTimeout("slow"))
    with pytest.raises(SourceUnavailableError) as raised:
        await _search({"exa": EXA_KEY}, api).search("exa", "asthma", 10, deep=True)
    assert raised.value.code == "source_busy"
    assert len(api.sent) == 1


async def _no_read(url):
    raise AssertionError("provider results are not re-read in deep mode")


async def test_research_events_stream_provider_rows_and_errors():
    class NoRunner:
        async def run(self, capability, payload):
            raise AssertionError("no scraper runs for Exa")

    ok = _search({"exa": EXA_KEY}, FakeApi(ApiReply(200, EXA_REPLY)))
    events = [
        e
        async for e in research_events(
            SearchRequest(query="asthma", source="exa", mode="deep"),
            NoRunner(),
            _no_read,
            web_apis=ok,
        )
    ]
    assert [e["type"] for e in events][-2:] == ["results", "done"]
    assert [r["source"] for r in events[-2]["results"]] == ["exa", "exa"]

    missing = _search({}, FakeApi())
    events = [
        e
        async for e in research_events(
            SearchRequest(query="asthma", source="tavily"),
            NoRunner(),
            _no_read,
            web_apis=missing,
        )
    ]
    assert events[-1]["type"] == "error"
    assert events[-1]["code"] == "source_not_connected"
