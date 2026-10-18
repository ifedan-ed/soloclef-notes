"""Which chats were filed into an "Earlier chats" notebook.

The record is what makes the move reversible: the migration's downgrade (and
anyone auditing the move) can tell a filed chat from one started inside the
notebook, and it is how the workspace's "Earlier chats" notebook is found again
without trusting a title the user may have changed.
"""

from __future__ import annotations

from sqlalchemy import Column, ForeignKey, Integer, text
from sqlalchemy.dialects.postgresql import TIMESTAMP

from app.db import Base


class NotebookEarlierChat(Base):
    __tablename__ = "notebook_earlier_chats"

    thread_id = Column(
        Integer,
        ForeignKey("new_chat_threads.id", ondelete="CASCADE"),
        primary_key=True,
    )
    notebook_id = Column(
        Integer,
        ForeignKey("notebooks.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    moved_at = Column(
        TIMESTAMP(timezone=True), nullable=False, server_default=text("NOW()")
    )
