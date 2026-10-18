"""The notification types the API recognizes."""

from __future__ import annotations

from typing import Literal

# An administrator's announcement, delivered to each recipient's inbox.
SITE_ANNOUNCEMENT_TYPE = "site_announcement"

NotificationType = Literal[
    "connector_indexing",
    "connector_deletion",
    "document_processing",
    "new_mention",
    "comment_reply",
    "insufficient_credits",
    "site_announcement",
]

NotificationCategory = Literal["comments", "status", "announcements"]
