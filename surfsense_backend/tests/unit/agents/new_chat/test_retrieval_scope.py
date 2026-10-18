from types import SimpleNamespace

import pytest

from app.agents.chat.retrieval_scope import (
    RetrievalScope,
    denied_retrieval_tools,
    excluded_retrieval_subagents,
)

pytestmark = pytest.mark.unit


def test_documents_scope_removes_public_web_specialists() -> None:
    excluded = excluded_retrieval_subagents(RetrievalScope.DOCUMENTS)
    assert {"google_search", "web_crawler", "youtube"} <= excluded
    assert excluded_retrieval_subagents(RetrievalScope.WEB) == set()
    assert excluded_retrieval_subagents(RetrievalScope.ALL) == set()


def test_documents_scope_denies_connected_app_reads_but_not_writes() -> None:
    tools = [
        SimpleNamespace(
            name="search_messages",
            metadata={"mcp_transport": "http", "hitl": False},
        ),
        SimpleNamespace(
            name="create_issue",
            metadata={"mcp_transport": "http", "hitl": True},
        ),
        SimpleNamespace(
            name="unknown_generic_tool",
            metadata={"mcp_transport": "stdio", "hitl": True, "mcp_is_generic": True},
        ),
    ]

    denied = denied_retrieval_tools(
        RetrievalScope.DOCUMENTS,
        subagent_name="mcp_discovery",
        tools=tools,
    )

    assert "search_messages" in denied
    assert "create_issue" not in denied
    assert "unknown_generic_tool" in denied


def test_web_scope_denies_knowledge_reads_but_not_writes() -> None:
    denied = denied_retrieval_tools(
        RetrievalScope.WEB,
        subagent_name="knowledge_base",
        tools=[],
    )

    assert {"search_knowledge_base", "read_file", "execute_code"} <= denied
    assert "write_file" not in denied


def test_web_scope_denies_every_knowledge_read_including_glob_and_grep() -> None:
    denied = denied_retrieval_tools(
        RetrievalScope.WEB, subagent_name="knowledge_base", tools=[]
    )

    assert {"glob", "grep", "ls", "list_tree", "search_knowledge_base"} <= denied


def test_a_scope_denial_tells_the_model_to_stop_not_that_it_lacks_permission() -> None:
    """A bare "Permission denied" made the agent retry the same task in a loop."""
    from app.agents.chat.multi_agent_chat.shared.permissions.deny import (
        build_deny_message,
    )
    from app.agents.chat.multi_agent_chat.shared.permissions.model import Rule
    from app.agents.chat.retrieval_scope import retrieval_denial_reason

    reason = retrieval_denial_reason(RetrievalScope.WEB, subagent_name="knowledge_base")
    rule = Rule(
        permission="search_knowledge_base", pattern="*", action="deny", reason=reason
    )
    message = build_deny_message(
        {"name": "search_knowledge_base", "id": "c1", "args": {}}, rule
    )

    text = str(message.content)
    assert "Permission denied" not in text
    assert "permission" not in text.lower()
    assert "'web'" in text
    assert "do not retry" in text
    assert "Documents or All" in text
    assert message.additional_kwargs["error"]["retryable"] is False
    # The reason is not part of matching: rules with and without it are equal.
    assert rule == Rule(permission="search_knowledge_base", pattern="*", action="deny")


def test_a_rule_without_a_reason_keeps_the_old_message() -> None:
    from app.agents.chat.multi_agent_chat.shared.permissions.deny import (
        build_deny_message,
    )
    from app.agents.chat.multi_agent_chat.shared.permissions.model import Rule

    message = build_deny_message(
        {"name": "rm", "id": "c1", "args": {}},
        Rule(permission="rm", pattern="*", action="deny"),
    )
    assert str(message.content) == "Permission denied: rule rm/* blocked tool 'rm'."
