"""Tables behind notebooks.

A notebook owns links, never content: deleting one removes its source links,
notes and studio-output links, while the documents, chats and artifacts they
point at stay in the workspace.
"""

from __future__ import annotations

from datetime import UTC, datetime

from sqlalchemy import (
    Boolean,
    CheckConstraint,
    Column,
    ForeignKey,
    Integer,
    String,
    Text,
    UniqueConstraint,
    text,
)
from sqlalchemy.dialects.postgresql import JSONB, TIMESTAMP, UUID
from sqlalchemy.orm import relationship

from app.db import BaseModel, TimestampMixin


def _now() -> datetime:
    return datetime.now(UTC)


class Notebook(BaseModel, TimestampMixin):
    __tablename__ = "notebooks"
    __table_args__ = (
        CheckConstraint(
            "chat_goal IN ('default', 'learning_guide', 'custom')",
            name="ck_notebooks_chat_goal",
        ),
        CheckConstraint(
            "response_length IN ('default', 'longer', 'shorter')",
            name="ck_notebooks_response_length",
        ),
        CheckConstraint(
            "title_source IN ('auto', 'user')", name="ck_notebooks_title_source"
        ),
        CheckConstraint(
            "icon_source IN ('auto', 'user')", name="ck_notebooks_icon_source"
        ),
    )

    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    title = Column(String(200), nullable=False)
    icon = Column(String(16), nullable=True)
    created_by_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="SET NULL"),
        nullable=True,
    )
    updated_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=_now,
        onupdate=_now,
        server_default=text("NOW()"),
        index=True,
    )
    # Cached focus suggestions for the studio, keyed by the source set they
    # were generated from so a changed source list regenerates them.
    focus_suggestions = Column(JSONB, nullable=True)
    focus_suggestions_key = Column(String(64), nullable=True)
    # How the notebook's chats answer ("Configure chat"); read on every turn.
    chat_goal = Column(
        String(16), nullable=False, default="default", server_default="default"
    )
    chat_instructions = Column(Text, nullable=True)
    response_length = Column(
        String(16), nullable=False, default="default", server_default="default"
    )
    # Automatic naming (app/notebooks/autoname): "auto" while the app may
    # still name the notebook, "user" for good once the user has renamed it
    # (or re-picked the icon). Never inferred from the text after creation.
    title_source = Column(
        String(8), nullable=False, default="user", server_default="user"
    )
    icon_source = Column(
        String(8), nullable=False, default="user", server_default="user"
    )
    # {"runs": n, "at": iso time, "sources": ready sources used, 0 = question}
    auto_title = Column(JSONB, nullable=True)

    sources = relationship(
        "NotebookSource",
        back_populates="notebook",
        cascade="all, delete-orphan",
        passive_deletes=True,
    )
    notes = relationship(
        "NotebookNote",
        back_populates="notebook",
        cascade="all, delete-orphan",
        passive_deletes=True,
    )


class NotebookSource(BaseModel):
    """One document or folder the notebook treats as primary context."""

    __tablename__ = "notebook_sources"
    __table_args__ = (
        CheckConstraint(
            "num_nonnulls(document_id, folder_id) = 1",
            name="ck_notebook_sources_one_target",
        ),
        UniqueConstraint(
            "notebook_id", "document_id", name="uq_notebook_sources_document"
        ),
        UniqueConstraint("notebook_id", "folder_id", name="uq_notebook_sources_folder"),
    )

    notebook_id = Column(
        Integer,
        ForeignKey("notebooks.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    document_id = Column(
        Integer,
        ForeignKey("documents.id", ondelete="CASCADE"),
        nullable=True,
        index=True,
    )
    folder_id = Column(
        Integer,
        ForeignKey("folders.id", ondelete="CASCADE"),
        nullable=True,
        index=True,
    )
    enabled = Column(Boolean, nullable=False, default=True, server_default="true")
    added_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=_now,
        server_default=text("NOW()"),
    )

    notebook = relationship("Notebook", back_populates="sources")


class NotebookNote(BaseModel, TimestampMixin):
    __tablename__ = "notebook_notes"
    __table_args__ = (
        CheckConstraint(
            "origin IN ('hand', 'chat', 'automation')",
            name="ck_notebook_notes_origin",
        ),
    )

    notebook_id = Column(
        Integer,
        ForeignKey("notebooks.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    title = Column(String(300), nullable=False)
    content = Column(Text, nullable=False, default="", server_default="")
    created_by_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="SET NULL"),
        nullable=True,
    )
    converted_document_id = Column(
        Integer,
        ForeignKey("documents.id", ondelete="SET NULL"),
        nullable=True,
    )
    # How the note came to be, shown on the Notes page: "hand" (Add note),
    # "chat" (Save to note under an answer, or a note the assistant wrote
    # that was moved in), "automation" (an automation's "Save results to
    # notebook", or an automation's file note moved in).
    origin = Column(String(16), nullable=False, default="hand", server_default="hand")
    # The automation that wrote it. Deleting the automation keeps the note
    # (and its "automation" origin) and only drops this link.
    automation_id = Column(
        Integer,
        ForeignKey("automations.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    updated_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=_now,
        onupdate=_now,
        server_default=text("NOW()"),
    )

    notebook = relationship("Notebook", back_populates="notes")


class NotebookOutput(BaseModel, TimestampMixin):
    """A studio generation started from a notebook.

    The generation itself lives where it always has — a podcast run for audio,
    a deliverable job for everything else — and this row is what ties it to
    the notebook, so the studio list does not depend on any chat.
    """

    __tablename__ = "notebook_outputs"
    __table_args__ = (
        CheckConstraint(
            "num_nonnulls(podcast_id, deliverable_job_id) <= 1",
            name="ck_notebook_outputs_one_run",
        ),
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
    kind = Column(String(32), nullable=False)
    title = Column(String(500), nullable=False)
    options = Column(JSONB, nullable=False, default=dict, server_default="{}")
    source_document_ids = Column(
        JSONB, nullable=False, default=list, server_default="[]"
    )
    podcast_id = Column(
        Integer,
        ForeignKey("podcast_runs.id", ondelete="SET NULL"),
        nullable=True,
    )
    deliverable_job_id = Column(
        Integer,
        ForeignKey("deliverable_jobs.id", ondelete="SET NULL"),
        nullable=True,
    )
    thread_id = Column(
        Integer,
        ForeignKey("new_chat_threads.id", ondelete="SET NULL"),
        nullable=True,
    )
    created_by_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="SET NULL"),
        nullable=True,
    )
    # Which model wrote it, and whether that was the fallback (c0006).
    generated_by = Column(JSONB, nullable=True)


class SourceGuide(BaseModel, TimestampMixin):
    """A cached short summary of a document, regenerated when its content changes."""

    __tablename__ = "document_source_guides"

    document_id = Column(
        Integer,
        ForeignKey("documents.id", ondelete="CASCADE"),
        nullable=False,
        unique=True,
    )
    content_hash = Column(String(128), nullable=False)
    summary = Column(Text, nullable=False)


class NotebookReportTemplate(BaseModel, TimestampMixin):
    """A workspace's edit of a seeded report template.

    Seeded templates are data in ``app.notebooks.studio.report_templates``; a
    row here overrides one of them for this workspace.
    """

    __tablename__ = "notebook_report_templates"
    __table_args__ = (
        UniqueConstraint(
            "workspace_id", "template_key", name="uq_notebook_report_templates_key"
        ),
    )

    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    template_key = Column(String(64), nullable=False)
    title = Column(String(200), nullable=False)
    description = Column(String(500), nullable=False, default="", server_default="")
    prompt = Column(Text, nullable=False)
    updated_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=_now,
        onupdate=_now,
        server_default=text("NOW()"),
    )


class NotebookFeature(BaseModel):
    """A notebook one user pinned to the "Featured notebooks" row of the home."""

    __tablename__ = "notebook_features"
    __table_args__ = (
        UniqueConstraint("notebook_id", "user_id", name="uq_notebook_features_user"),
    )

    notebook_id = Column(
        Integer,
        ForeignKey("notebooks.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    user_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    featured_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=_now,
        server_default=text("NOW()"),
    )
