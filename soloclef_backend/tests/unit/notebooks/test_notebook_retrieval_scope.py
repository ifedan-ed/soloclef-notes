"""A notebook chat answers from its sources only, like NotebookLM.

The composer's old Sources / + Web switch survives as a workspace-wide cookie,
so a notebook turn can still arrive asking for "web" or "all". Whatever it
asks, the turn reads the notebook's sources and never retrieves from the web.
Only a deliberate per-message pick widens it: a Tools-menu tool (that one web
specialist) or an @-mentioned connected account. Plain chats (automations)
keep the requested scope.
"""

from __future__ import annotations

import pytest

from app.agents.chat.retrieval_scope import (
    WEB_RETRIEVAL_SUBAGENTS,
    RetrievalScope,
    SourcesOnly,
    denied_retrieval_tools,
    excluded_retrieval_subagents,
    retrieval_scope_prompt,
)
from app.agents.chat.tool_preference import ToolPreference
from app.notebooks.chat.pins import NotebookPins, notebook_retrieval_scope

pytestmark = pytest.mark.unit

PINS = NotebookPins(notebook_id=1, title="n", document_ids=[7], scope_document_ids=[7])


@pytest.mark.parametrize("requested", list(RetrievalScope))
def test_a_notebook_chat_is_documents_only_whatever_it_asks_for(requested):
    scope, sources_only = notebook_retrieval_scope(requested, PINS)

    assert scope is RetrievalScope.DOCUMENTS
    assert sources_only == SourcesOnly()
    # No web specialist is built, the sources stay readable, connected-app
    # reads are off.
    assert excluded_retrieval_subagents(scope, sources_only) == set(
        WEB_RETRIEVAL_SUBAGENTS
    )
    assert (
        denied_retrieval_tools(scope, subagent_name="knowledge_base", tools=[]) == set()
    )
    assert "get_connected_accounts" in denied_retrieval_tools(
        scope, subagent_name="mcp_discovery", tools=[]
    )


def test_an_empty_notebook_is_still_sources_only():
    empty = NotebookPins(notebook_id=1, title="n")
    assert notebook_retrieval_scope(RetrievalScope.WEB, empty) == (
        RetrievalScope.DOCUMENTS,
        SourcesOnly(),
    )


@pytest.mark.parametrize("requested", list(RetrievalScope))
def test_plain_chats_keep_the_requested_scope(requested):
    assert notebook_retrieval_scope(requested, None) == (requested, None)
    assert notebook_retrieval_scope(
        requested, None, preferred_tool=ToolPreference.REDDIT
    ) == (requested, None)


def test_a_tools_menu_pick_adds_only_that_specialist():
    # The request validator has already widened a tool turn to "all".
    scope, sources_only = notebook_retrieval_scope(
        RetrievalScope.ALL, PINS, preferred_tool=ToolPreference.GOOGLE_SEARCH
    )

    assert scope is RetrievalScope.DOCUMENTS
    assert sources_only.picked_specialists == {"google_search"}
    assert excluded_retrieval_subagents(scope, sources_only) == (
        set(WEB_RETRIEVAL_SUBAGENTS) - {"google_search"}
    )
    # "web" (the crawler) maps to its own specialist.
    _, crawl = notebook_retrieval_scope(
        RetrievalScope.WEB, PINS, preferred_tool=ToolPreference.WEB
    )
    assert crawl.picked_specialists == {"web_crawler"}


def test_a_connected_account_mention_reads_that_app_but_not_the_web():
    scope, sources_only = notebook_retrieval_scope(
        RetrievalScope.ALL, PINS, mentions_connector=True
    )

    assert scope is RetrievalScope.ALL
    assert excluded_retrieval_subagents(scope, sources_only) == set(
        WEB_RETRIEVAL_SUBAGENTS
    )
    assert (
        denied_retrieval_tools(scope, subagent_name="mcp_discovery", tools=[]) == set()
    )


def test_the_notebook_prompt_rules_out_the_web():
    prompt = retrieval_scope_prompt(RetrievalScope.DOCUMENTS, SourcesOnly())

    assert "This is a notebook chat" in prompt
    assert "only from the notebook's enabled sources" in prompt
    assert "Do not search the web or offer to" in prompt
    assert "Search the web for new sources" in prompt
    assert "picked the" not in prompt
    # Plain chats keep the old wording.
    assert "notebook" not in retrieval_scope_prompt(RetrievalScope.DOCUMENTS)


def test_the_notebook_prompt_names_a_picked_tool_as_web_not_a_source():
    prompt = retrieval_scope_prompt(
        RetrievalScope.DOCUMENTS, SourcesOnly.picking(["reddit"])
    )

    assert "picked the reddit tool" in prompt
    assert "task(reddit, ...) is available" in prompt
    assert "comes from the web, not from the notebook's sources" in prompt
    assert "never with a source's [n]" in prompt


def test_picking_ignores_non_web_specialists():
    assert SourcesOnly.picking(["knowledge_base", "youtube"]).picked_specialists == {
        "youtube"
    }


def test_cache_token_separates_picks():
    assert SourcesOnly().cache_token() != SourcesOnly.picking(["youtube"]).cache_token()
    assert (
        SourcesOnly.picking(["youtube", "reddit"]).cache_token()
        == SourcesOnly.picking(["reddit", "youtube"]).cache_token()
    )


@pytest.mark.parametrize(
    ("preference", "specialist"),
    [
        (ToolPreference.EXA, "exa_search"),
        (ToolPreference.TAVILY, "tavily_search"),
        (ToolPreference.BROWSERBASE, "browserbase"),
    ],
)
def test_picking_a_keyed_web_tool_adds_it_and_nothing_else(preference, specialist):
    scope, sources_only = notebook_retrieval_scope(
        RetrievalScope.ALL, PINS, preferred_tool=preference
    )

    assert scope is RetrievalScope.DOCUMENTS
    assert sources_only.picked_specialists == {specialist}
    assert excluded_retrieval_subagents(scope, sources_only) == (
        set(WEB_RETRIEVAL_SUBAGENTS) - {specialist}
    )
    # Nothing else opens: the MCP servers' own read tools stay off, and the
    # prompt still says connected-app data is not retrieved. (Mail is held
    # back by that prompt line only; see custom/README.md.)
    assert "get_connected_accounts" in denied_retrieval_tools(
        scope, subagent_name="mcp_discovery", tools=[]
    )
    prompt = retrieval_scope_prompt(scope, sources_only)
    assert "Connected-app data is not retrieved either" in prompt
    assert f"picked the {specialist} tool" in prompt
    assert "comes from the web, not from the notebook's sources" in prompt


def test_a_picked_keyed_tool_without_a_key_is_not_offered_to_the_model():
    from app.agents.chat.keyed_web_specialists import excluded_keyed_web_specialists

    scope, picked = notebook_retrieval_scope(
        RetrievalScope.ALL, PINS, preferred_tool=ToolPreference.TAVILY
    )
    # The workspace has an Exa key only.
    sources_only = picked.without(excluded_keyed_web_specialists({"exa_search"}))

    assert sources_only.picked_specialists == frozenset()
    assert sources_only.unavailable_picks == {"tavily_search"}
    assert "tavily_search" in excluded_retrieval_subagents(scope, sources_only)
    prompt = retrieval_scope_prompt(scope, sources_only)
    assert "task(tavily_search" not in prompt
    assert "picked the tavily_search tool in the composer, but" in prompt
    assert "not available and must not be called" in prompt
    # A different graph from both "Tavily available" and "nothing picked".
    assert sources_only.cache_token() not in {
        picked.cache_token(),
        SourcesOnly().cache_token(),
    }


def test_a_picked_tool_that_can_be_built_is_left_as_it_is():
    from app.agents.chat.keyed_web_specialists import excluded_keyed_web_specialists

    _, picked = notebook_retrieval_scope(
        RetrievalScope.ALL, PINS, preferred_tool=ToolPreference.EXA
    )

    assert picked.without(excluded_keyed_web_specialists({"exa_search"})) is picked
    _, youtube = notebook_retrieval_scope(
        RetrievalScope.ALL, PINS, preferred_tool=ToolPreference.YOUTUBE
    )
    # Not a keyed tool: no key needed, never dropped.
    assert youtube.without(excluded_keyed_web_specialists(())) is youtube
