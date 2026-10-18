"""Exa and Tavily as chat tools: one search on the workspace's own key.

Only the providers' HTTP APIs (the ``send`` seam) and the key lookup are
stood in; the request building, parsing and error wording are research's own.
"""

from __future__ import annotations

import httpx
import pytest

from app.capabilities.core.types import BillingUnit
from app.capabilities.core.unavailable import CapabilityUnavailableError
from app.capabilities.web_search_api.definitions import web_search_capability
from app.capabilities.web_search_api.executor import build_web_search_executor
from app.capabilities.web_search_api.schemas import WebSearchInput
from app.notebooks.research.web_apis.requests import ApiReply

pytestmark = pytest.mark.unit

EXA_REPLY = {
    "results": [
        {
            "title": "School nurses and asthma",
            "url": "https://www.example.org/asthma",
            "publishedDate": "2024-05-01T00:00:00.000Z",
            "highlights": ["Nurses manage asthma at school."],
        },
        {"title": "Not a web page", "url": "ftp://nope"},
    ]
}
TAVILY_REPLY = {
    "results": [
        {
            "title": "Asthma action plans",
            "url": "https://www.cdc.example/asthma",
            "content": "An asthma action plan helps.",
        }
    ]
}


class FakeApi:
    def __init__(self, reply: ApiReply | None = None, error: Exception | None = None):
        self.reply = reply
        self.error = error
        self.sent = []

    async def __call__(self, request):
        self.sent.append(request)
        if self.error:
            raise self.error
        return self.reply


def _keys(**keys):
    async def lookup():
        return keys

    return lookup


async def test_exa_runs_its_auto_search_with_the_workspace_key():
    api = FakeApi(ApiReply(status=200, data=EXA_REPLY))
    execute = build_web_search_executor(
        "exa", 7, lookup_keys=_keys(exa="exa-key"), send=api
    )

    output = await execute(WebSearchInput(query="asthma at school"))

    request = api.sent[0]
    assert request.url == "https://api.exa.ai/search"
    assert request.headers["x-api-key"] == "exa-key"
    assert request.body["type"] == "auto"
    assert request.body["numResults"] == 10
    assert [item.url for item in output.items] == ["https://www.example.org/asthma"]
    assert output.items[0].snippet == "Nurses manage asthma at school."
    assert "2024-05-01" in (output.items[0].meta or "")
    assert output.provider == "exa"
    assert output.billable_units == 1


async def test_tavily_runs_a_basic_search():
    api = FakeApi(ApiReply(status=200, data=TAVILY_REPLY))
    execute = build_web_search_executor(
        "tavily", 7, lookup_keys=_keys(tavily="tvly-key"), send=api
    )

    output = await execute(WebSearchInput(query="asthma plan", max_results=3))

    request = api.sent[0]
    assert request.headers["Authorization"] == "Bearer tvly-key"
    assert request.body["search_depth"] == "basic"
    assert request.body["max_results"] == 3
    assert output.items[0].title == "Asthma action plans"


async def test_a_search_with_no_results_is_still_one_billed_search():
    api = FakeApi(ApiReply(status=200, data={"results": []}))
    execute = build_web_search_executor("exa", 7, lookup_keys=_keys(exa="k"), send=api)

    output = await execute(WebSearchInput(query="nothing"))

    assert output.items == []
    assert output.billable_units == 1


async def test_without_a_key_nothing_is_sent_and_the_person_is_told():
    api = FakeApi(ApiReply(status=200, data=EXA_REPLY))
    execute = build_web_search_executor("exa", 7, lookup_keys=_keys(), send=api)

    with pytest.raises(CapabilityUnavailableError, match="isn't connected"):
        await execute(WebSearchInput(query="q"))
    assert api.sent == []


@pytest.mark.parametrize(
    ("status", "words"),
    [
        (401, "refused this workspace's API key"),
        (429, "out of credit or over its rate limit"),
        (432, "out of credit or over its rate limit"),
        (503, "didn't answer just now"),
        (400, "couldn't run this search"),
    ],
)
async def test_provider_refusals_become_a_sentence_not_a_crash(status, words):
    execute = build_web_search_executor(
        "tavily",
        7,
        lookup_keys=_keys(tavily="k"),
        send=FakeApi(ApiReply(status=status, data={})),
    )

    with pytest.raises(CapabilityUnavailableError, match=words):
        await execute(WebSearchInput(query="q"))


async def test_an_unreachable_provider_becomes_a_sentence():
    execute = build_web_search_executor(
        "exa",
        7,
        lookup_keys=_keys(exa="k"),
        send=FakeApi(error=httpx.ConnectError("down")),
    )

    with pytest.raises(CapabilityUnavailableError, match="didn't answer just now"):
        await execute(WebSearchInput(query="q"))


def test_input_is_bounded_and_one_search_is_estimated():
    payload = WebSearchInput(query="  q  ")
    assert payload.estimated_units == 1
    with pytest.raises(ValueError):
        WebSearchInput(query="q", max_results=11)
    with pytest.raises(ValueError):
        WebSearchInput(query="x" * 401)


@pytest.mark.parametrize(
    ("provider", "unit", "tool"),
    [
        ("exa", BillingUnit.EXA_SEARCH, "exa_web_search"),
        ("tavily", BillingUnit.TAVILY_SEARCH, "tavily_web_search"),
    ],
)
def test_each_provider_is_billed_on_its_own_meter(provider, unit, tool):
    capability = web_search_capability(provider, 7)

    assert capability.billing_unit is unit
    assert capability.name.replace(".", "_") == tool
    assert capability.activity is not None
    assert capability.activity.integration_key == provider
