"""``email_imap`` native tools and (empty) permission ruleset.

All four tools are read-only; there is nothing here that mutates mail, so the
ruleset has no approval-gated rules (unlike google_drive's create/delete).
"""

from __future__ import annotations

from typing import Any

from langchain_core.tools import BaseTool

from app.agents.chat.multi_agent_chat.shared.permissions import Ruleset
from app.capabilities.core.types import ActivityDescriptor

from .list_accounts import create_list_email_accounts_tool
from .list_folders import create_list_email_folders_tool
from .read_email import create_read_email_tool
from .search_emails import create_search_emails_tool

NAME = "email_imap"

RULESET = Ruleset(origin=NAME, rules=[])


def load_tools(
    *, dependencies: dict[str, Any] | None = None, **kwargs: Any
) -> list[BaseTool]:
    d = {**(dependencies or {}), **kwargs}
    common = {
        "db_session": d["db_session"],
        "workspace_id": d["workspace_id"],
        "user_id": d["user_id"],
    }
    tools = [
        create_list_email_accounts_tool(**common),
        create_list_email_folders_tool(**common),
        create_search_emails_tool(**common),
        create_read_email_tool(**common),
    ]
    for tool in tools:
        descriptor = _ACTIVITY.get(tool.name)
        if descriptor is not None:
            tool.metadata = {
                **(tool.metadata or {}),
                "activity_descriptor": descriptor.as_metadata(),
            }
    return tools


# What the chat's step list says while each tool runs and once it is done;
# without these every email step read "Using a tool" / "Completed an action".
_ACTIVITY: dict[str, ActivityDescriptor] = {
    "list_email_accounts": ActivityDescriptor(
        active_title="Listing email accounts",
        completed_title="Listed email accounts",
        category="connector",
        icon_key="list-todo",
        kind="email.accounts",
    ),
    "list_email_folders": ActivityDescriptor(
        active_title="Listing email folders",
        completed_title="Listed email folders",
        category="connector",
        icon_key="folder-open",
        kind="email.folders",
    ),
    "search_emails": ActivityDescriptor(
        active_title="Searching email",
        completed_title="Searched email",
        category="connector",
        icon_key="search",
        kind="email.search",
    ),
    "read_email": ActivityDescriptor(
        active_title="Reading an email",
        completed_title="Read an email",
        category="connector",
        icon_key="file-text",
        kind="email.read",
    ),
}
