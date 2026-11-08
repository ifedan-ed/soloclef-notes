"""The composer's Tools menu: one platform for one message."""

from __future__ import annotations

import pytest
from pydantic import ValidationError

from app.agents.chat.retrieval_scope import (
    WEB_RETRIEVAL_SUBAGENTS,
    RetrievalScope,
)
from app.agents.chat.tool_preference import (
    ToolPreference,
    render_tool_preference,
    scope_for_tool_preference,
    specialist_for,
)
from app.schemas.new_chat import NewChatRequest

pytestmark = pytest.mark.unit


def test_every_preference_routes_to_a_real_web_specialist():
    for preference in ToolPreference:
        assert specialist_for(preference) in WEB_RETRIEVAL_SUBAGENTS


def test_a_forced_platform_widens_a_documents_only_turn():
    assert (
        scope_for_tool_preference(RetrievalScope.DOCUMENTS, ToolPreference.YOUTUBE)
        is RetrievalScope.ALL
    )
    assert (
        scope_for_tool_preference(RetrievalScope.WEB, ToolPreference.YOUTUBE)
        is RetrievalScope.WEB
    )
    assert (
        scope_for_tool_preference(RetrievalScope.DOCUMENTS, None)
        is RetrievalScope.DOCUMENTS
    )


def test_block_names_the_specialist():
    block = render_tool_preference(ToolPreference.REDDIT)
    assert block.startswith("<tool_preference>") and "reddit specialist" in block
    assert render_tool_preference(None) is None


def test_the_web_tool_is_described_as_a_page_reader_not_a_search():
    block = render_tool_preference(ToolPreference.WEB)
    assert "web search" not in block
    assert "cannot search the web" in block
    assert "Google Search tool searches" in block


def test_request_accepts_and_validates_the_preference():
    request = NewChatRequest(
        chat_id=1, user_query="hi", workspace_id=1, preferred_tool="youtube"
    )
    assert request.preferred_tool is ToolPreference.YOUTUBE
    assert request.retrieval_scope is RetrievalScope.ALL
    with pytest.raises(ValidationError):
        NewChatRequest(chat_id=1, user_query="hi", workspace_id=1, preferred_tool="x")


@pytest.mark.parametrize(
    ("value", "label", "specialist"),
    [
        ("exa", "Exa", "exa_search"),
        ("tavily", "Tavily", "tavily_search"),
        ("browserbase", "Browserbase", "browserbase"),
    ],
)
def test_the_keyed_web_tools_can_be_picked(value, label, specialist):
    request = NewChatRequest(
        chat_id=1, user_query="hi", workspace_id=1, preferred_tool=value
    )
    block = render_tool_preference(request.preferred_tool)

    assert specialist_for(request.preferred_tool) == specialist
    assert f"chose the {label} tool" in block
    assert f"{specialist} specialist" in block
