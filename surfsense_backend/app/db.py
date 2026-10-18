import logging
import uuid
from collections.abc import AsyncGenerator
from contextlib import asynccontextmanager
from datetime import UTC, datetime
from enum import StrEnum

import anyio
from fastapi import Depends
from fastapi_users.db import SQLAlchemyBaseUserTableUUID, SQLAlchemyUserDatabase
from pgvector.sqlalchemy import Vector
from sqlalchemy import (
    ARRAY,
    JSON,
    TIMESTAMP,
    BigInteger,
    Boolean,
    CheckConstraint,
    Column,
    Enum as SQLAlchemyEnum,
    ForeignKey,
    Index,
    Integer,
    String,
    Text,
    UniqueConstraint,
    func,
    text,
)
from sqlalchemy.dialects.postgresql import JSONB, UUID
from sqlalchemy.ext.asyncio import AsyncSession, async_sessionmaker, create_async_engine
from sqlalchemy.orm import DeclarativeBase, Mapped, backref, declared_attr, relationship

from app.config import config

if config.AUTH_TYPE == "GOOGLE":
    from fastapi_users.db import SQLAlchemyBaseOAuthAccountTableUUID

logger = logging.getLogger(__name__)

DATABASE_URL = config.DATABASE_URL


class DocumentType(StrEnum):
    EXTENSION = "EXTENSION"
    CRAWLED_URL = "CRAWLED_URL"
    FILE = "FILE"
    YOUTUBE_VIDEO = "YOUTUBE_VIDEO"
    GOOGLE_DRIVE_FILE = "GOOGLE_DRIVE_FILE"
    NOTE = "NOTE"
    ARTIFACT = "ARTIFACT"
    # Files of a folder uploaded from the browser (or synced by the desktop app).
    LOCAL_FOLDER_FILE = "LOCAL_FOLDER_FILE"


class SearchSourceConnectorType(StrEnum):
    GOOGLE_DRIVE_CONNECTOR = "GOOGLE_DRIVE_CONNECTOR"
    WEBCRAWLER_CONNECTOR = "WEBCRAWLER_CONNECTOR"
    MCP_CONNECTOR = "MCP_CONNECTOR"  # Model Context Protocol - User-defined API tools
    # Picker only: files are imported once on selection, never synced.
    NEXTCLOUD_CONNECTOR = "NEXTCLOUD_CONNECTOR"
    # Read-only, live (never indexed): searched/read per chat turn.
    EMAIL_IMAP_CONNECTOR = "EMAIL_IMAP_CONNECTOR"


class VideoPresentationStatus(StrEnum):
    PENDING = "pending"
    GENERATING = "generating"
    READY = "ready"
    FAILED = "failed"


class DeliverableJobStatus(StrEnum):
    QUEUED = "queued"
    RUNNING = "running"
    CANCELLING = "cancelling"
    CANCELLED = "cancelled"
    READY = "ready"
    FAILED = "failed"


class DeliverableFailureCode(StrEnum):
    DURATION_LIMIT = "duration_limit"
    QUOTA_EXCEEDED = "quota_exceeded"
    GENERATION_FAILED = "generation_failed"
    RENDER_FAILED = "render_failed"
    VERIFICATION_FAILED = "verification_failed"
    CANCELLED = "cancelled"


class DocumentStatus:
    """
    Helper class for document processing status (stored as JSONB).

    Status values:
    - {"state": "ready"} - Document is fully processed and searchable
    - {"state": "pending"} - Document is queued, waiting to be processed
    - {"state": "processing"} - Document is currently being processed (only 1 at a time)
    - {"state": "failed", "reason": "..."} - Processing failed with reason

    Usage:
        document.status = DocumentStatus.pending()
        document.status = DocumentStatus.processing()
        document.status = DocumentStatus.ready()
        document.status = DocumentStatus.failed("LLM rate limit exceeded")
    """

    # State constants
    READY = "ready"
    PENDING = "pending"
    PROCESSING = "processing"
    FAILED = "failed"

    @staticmethod
    def ready() -> dict:
        """Return status dict for a ready/searchable document."""
        return {"state": DocumentStatus.READY}

    @staticmethod
    def pending() -> dict:
        """Return status dict for a document waiting to be processed."""
        return {"state": DocumentStatus.PENDING}

    @staticmethod
    def processing() -> dict:
        """Return status dict for a document being processed."""
        return {"state": DocumentStatus.PROCESSING}

    @staticmethod
    def failed(reason: str, **extra_details) -> dict:
        """
        Return status dict for a failed document.

        Args:
            reason: Human-readable failure reason
            **extra_details: Optional additional details (duplicate_of, error_code, etc.)
        """
        status = {
            "state": DocumentStatus.FAILED,
            "reason": reason[:500],
        }  # Truncate long reasons
        if extra_details:
            status.update(extra_details)
        return status

    @staticmethod
    def get_state(status: dict | None) -> str | None:
        """Extract state from status dict, returns None if invalid."""
        if status is None:
            return None
        return status.get("state") if isinstance(status, dict) else None

    @staticmethod
    def is_state(status: dict | None, state: str) -> bool:
        """Check if status matches a given state."""
        return DocumentStatus.get_state(status) == state

    @staticmethod
    def get_failure_reason(status: dict | None) -> str | None:
        """Extract failure reason from status dict."""
        if status is None or not isinstance(status, dict):
            return None
        if status.get("state") == DocumentStatus.FAILED:
            return status.get("reason")
        return None


class ConnectionScope(StrEnum):
    GLOBAL = "GLOBAL"
    SEARCH_SPACE = "SEARCH_SPACE"
    USER = "USER"


class ModelSource(StrEnum):
    DISCOVERED = "DISCOVERED"
    MANUAL = "MANUAL"


class LogLevel(StrEnum):
    DEBUG = "DEBUG"
    INFO = "INFO"
    WARNING = "WARNING"
    ERROR = "ERROR"
    CRITICAL = "CRITICAL"


class LogStatus(StrEnum):
    IN_PROGRESS = "IN_PROGRESS"
    SUCCESS = "SUCCESS"
    FAILED = "FAILED"


class Permission(StrEnum):
    """
    Granular permissions for workspace resources.
    Use '*' (FULL_ACCESS) to grant all permissions.
    """

    # Documents
    DOCUMENTS_CREATE = "documents:create"
    DOCUMENTS_READ = "documents:read"
    DOCUMENTS_UPDATE = "documents:update"
    DOCUMENTS_DELETE = "documents:delete"

    # Artifacts
    ARTIFACTS_CREATE = "artifacts:create"
    ARTIFACTS_READ = "artifacts:read"
    ARTIFACTS_UPDATE = "artifacts:update"
    ARTIFACTS_DELETE = "artifacts:delete"

    # Chats
    CHATS_CREATE = "chats:create"
    CHATS_READ = "chats:read"
    CHATS_UPDATE = "chats:update"
    CHATS_DELETE = "chats:delete"

    # Comments
    COMMENTS_CREATE = "comments:create"
    COMMENTS_READ = "comments:read"
    COMMENTS_DELETE = "comments:delete"

    # LLM Configs
    LLM_CONFIGS_CREATE = "llm_configs:create"
    LLM_CONFIGS_READ = "llm_configs:read"
    LLM_CONFIGS_UPDATE = "llm_configs:update"
    LLM_CONFIGS_DELETE = "llm_configs:delete"

    # Podcasts
    PODCASTS_CREATE = "podcasts:create"
    PODCASTS_READ = "podcasts:read"
    PODCASTS_UPDATE = "podcasts:update"
    PODCASTS_DELETE = "podcasts:delete"

    # Video Presentations
    VIDEO_PRESENTATIONS_CREATE = "video_presentations:create"
    VIDEO_PRESENTATIONS_READ = "video_presentations:read"
    VIDEO_PRESENTATIONS_UPDATE = "video_presentations:update"
    VIDEO_PRESENTATIONS_DELETE = "video_presentations:delete"

    # Vision LLM Configs
    VISION_CONFIGS_CREATE = "vision_configs:create"
    VISION_CONFIGS_READ = "vision_configs:read"
    VISION_CONFIGS_DELETE = "vision_configs:delete"

    # Connectors
    CONNECTORS_CREATE = "connectors:create"
    CONNECTORS_READ = "connectors:read"
    CONNECTORS_UPDATE = "connectors:update"
    CONNECTORS_DELETE = "connectors:delete"

    # Logs
    LOGS_READ = "logs:read"
    LOGS_DELETE = "logs:delete"

    # Members
    MEMBERS_INVITE = "members:invite"
    MEMBERS_VIEW = "members:view"
    MEMBERS_REMOVE = "members:remove"
    MEMBERS_MANAGE_ROLES = "members:manage_roles"

    # Roles
    ROLES_CREATE = "roles:create"
    ROLES_READ = "roles:read"
    ROLES_UPDATE = "roles:update"
    ROLES_DELETE = "roles:delete"

    # Workspace Settings
    SETTINGS_VIEW = "settings:view"
    SETTINGS_UPDATE = "settings:update"
    SETTINGS_DELETE = "settings:delete"  # Delete the entire workspace

    # API Access
    API_ACCESS_MANAGE = "api_access:manage"

    # Public Sharing
    PUBLIC_SHARING_VIEW = "public_sharing:view"
    PUBLIC_SHARING_CREATE = "public_sharing:create"
    PUBLIC_SHARING_DELETE = "public_sharing:delete"

    # Automations
    AUTOMATIONS_CREATE = "automations:create"
    AUTOMATIONS_READ = "automations:read"
    AUTOMATIONS_UPDATE = "automations:update"
    AUTOMATIONS_DELETE = "automations:delete"
    AUTOMATIONS_EXECUTE = "automations:execute"

    # Full access wildcard
    FULL_ACCESS = "*"


# Predefined role permission sets for convenience
# Note: Only Owner, Editor, and Viewer roles are supported.
# Owner has full access (*), Editor can do everything except delete, Viewer has read-only access.
DEFAULT_ROLE_PERMISSIONS = {
    "Owner": [Permission.FULL_ACCESS.value],
    "Editor": [
        # Documents (no delete)
        Permission.DOCUMENTS_CREATE.value,
        Permission.DOCUMENTS_READ.value,
        Permission.DOCUMENTS_UPDATE.value,
        # Artifacts (no delete)
        Permission.ARTIFACTS_CREATE.value,
        Permission.ARTIFACTS_READ.value,
        Permission.ARTIFACTS_UPDATE.value,
        # Chats (no delete)
        Permission.CHATS_CREATE.value,
        Permission.CHATS_READ.value,
        Permission.CHATS_UPDATE.value,
        # Comments (no delete)
        Permission.COMMENTS_CREATE.value,
        Permission.COMMENTS_READ.value,
        # LLM Configs (no delete)
        Permission.LLM_CONFIGS_CREATE.value,
        Permission.LLM_CONFIGS_READ.value,
        Permission.LLM_CONFIGS_UPDATE.value,
        # Podcasts (no delete)
        Permission.PODCASTS_CREATE.value,
        Permission.PODCASTS_READ.value,
        Permission.PODCASTS_UPDATE.value,
        # Video Presentations (no delete)
        Permission.VIDEO_PRESENTATIONS_CREATE.value,
        Permission.VIDEO_PRESENTATIONS_READ.value,
        Permission.VIDEO_PRESENTATIONS_UPDATE.value,
        # Vision Configs (create and read, no delete)
        Permission.VISION_CONFIGS_CREATE.value,
        Permission.VISION_CONFIGS_READ.value,
        # Connectors (no delete)
        Permission.CONNECTORS_CREATE.value,
        Permission.CONNECTORS_READ.value,
        Permission.CONNECTORS_UPDATE.value,
        # Logs (read only)
        Permission.LOGS_READ.value,
        # Members (can invite and view only, cannot manage roles or remove)
        Permission.MEMBERS_INVITE.value,
        Permission.MEMBERS_VIEW.value,
        # Roles (read only - cannot create, update, or delete)
        Permission.ROLES_READ.value,
        # Settings (view only, no update or delete)
        Permission.SETTINGS_VIEW.value,
        # Public Sharing (view only: public links are created by Owners, c0010)
        Permission.PUBLIC_SHARING_VIEW.value,
        # Automations (no delete)
        Permission.AUTOMATIONS_CREATE.value,
        Permission.AUTOMATIONS_READ.value,
        Permission.AUTOMATIONS_UPDATE.value,
        Permission.AUTOMATIONS_EXECUTE.value,
    ],
    "Viewer": [
        # Documents (read only)
        Permission.DOCUMENTS_READ.value,
        # Artifacts (read only)
        Permission.ARTIFACTS_READ.value,
        # Chats (read only)
        Permission.CHATS_READ.value,
        # Comments (can create and read, but not delete)
        Permission.COMMENTS_CREATE.value,
        Permission.COMMENTS_READ.value,
        # LLM Configs (read only)
        Permission.LLM_CONFIGS_READ.value,
        # Podcasts (read only)
        Permission.PODCASTS_READ.value,
        # Video Presentations (read only)
        Permission.VIDEO_PRESENTATIONS_READ.value,
        # Vision Configs (read only)
        Permission.VISION_CONFIGS_READ.value,
        # Connectors (read only)
        Permission.CONNECTORS_READ.value,
        # Logs (read only)
        Permission.LOGS_READ.value,
        # Members (view only)
        Permission.MEMBERS_VIEW.value,
        # Roles (read only)
        Permission.ROLES_READ.value,
        # Settings (view only)
        Permission.SETTINGS_VIEW.value,
        # Public Sharing (view only)
        Permission.PUBLIC_SHARING_VIEW.value,
        # Automations (read only)
        Permission.AUTOMATIONS_READ.value,
    ],
}


class Base(DeclarativeBase):
    pass


class TimestampMixin:
    @declared_attr
    def created_at(cls):  # noqa: N805
        return Column(
            TIMESTAMP(timezone=True),
            nullable=False,
            default=lambda: datetime.now(UTC),
            index=True,
        )


class BaseModel(Base):
    __abstract__ = True
    __allow_unmapped__ = True

    id = Column(Integer, primary_key=True, index=True)


class NewChatMessageRole(StrEnum):
    """Role enum for new chat messages."""

    USER = "user"
    ASSISTANT = "assistant"
    SYSTEM = "system"


class ChatVisibility(StrEnum):
    """
    Visibility/sharing level for chat threads.

    PRIVATE: Only the creator can see/access the chat (default)
    SEARCH_SPACE: All members of the workspace can see/access the chat
    PUBLIC: (Future) Anyone with the link can access the chat
    """

    PRIVATE = "PRIVATE"
    SEARCH_SPACE = "SEARCH_SPACE"
    # PUBLIC = "PUBLIC"  # Reserved for future implementation


# Threads minted by the artifact-generation API: a vessel for the subagent's
# sandbox key and artifact attribution, never streamed to, so the sidebar hides
# them by this source.
ARTIFACT_API_SOURCE = "artifact_api"


class NewChatThread(BaseModel, TimestampMixin):
    """
    Thread model for the new chat feature using assistant-ui.
    Each thread represents a conversation with message history.
    LangGraph checkpointer uses thread_id for state persistence.
    """

    __tablename__ = "new_chat_threads"

    title = Column(String(500), nullable=False, default="New Chat", index=True)
    archived = Column(Boolean, nullable=False, default=False)
    updated_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=lambda: datetime.now(UTC),
        onupdate=lambda: datetime.now(UTC),
        index=True,
    )

    # Visibility/sharing control
    visibility = Column(
        SQLAlchemyEnum(ChatVisibility),
        nullable=False,
        default=ChatVisibility.PRIVATE,
        server_default="PRIVATE",
        index=True,
    )

    # Foreign keys
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
    )

    # Track who created this chat thread (for visibility filtering)
    created_by_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="SET NULL"),
        nullable=True,  # Nullable for existing records before migration
        index=True,
    )

    # Clone tracking - for audit and history bootstrap
    cloned_from_thread_id = Column(
        Integer,
        ForeignKey("new_chat_threads.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    cloned_from_snapshot_id = Column(
        Integer,
        ForeignKey("public_chat_snapshots.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    cloned_at = Column(
        TIMESTAMP(timezone=True),
        nullable=True,
    )
    # Flag to bootstrap LangGraph checkpointer with DB messages on first message
    needs_history_bootstrap = Column(
        Boolean,
        nullable=False,
        default=False,
        server_default="false",
    )
    # Auto model pin for this thread: concrete resolved global LLM
    # config id. NULL means no pin; Auto will resolve on the next turn.
    # Single-writer invariant: only app.services.auto_model_pin_service sets
    # or clears this column (plus bulk clears when a workspace's
    # chat_model_id changes). Unindexed: all reads are by primary key.
    pinned_llm_config_id = Column(Integer, nullable=True)

    # Surface metadata for chat threads (first-party SurfSense today).
    # Zero publishes all chat-message sources; the UI can decide which surfaces to render.
    source = Column(
        Text, nullable=False, default="surfsense", server_default="surfsense"
    )
    # The notebook this conversation belongs to. Deleting the notebook
    # detaches the chat rather than deleting it: a conversation, and the
    # artifacts it produced, is the user's work.
    notebook_id = Column(
        Integer,
        ForeignKey("notebooks.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )

    # Relationships
    workspace = relationship("Workspace", back_populates="new_chat_threads")
    created_by = relationship("User", back_populates="new_chat_threads")
    messages = relationship(
        "NewChatMessage",
        back_populates="thread",
        order_by="NewChatMessage.created_at",
        cascade="all, delete-orphan",
    )
    snapshots = relationship(
        "PublicChatSnapshot",
        back_populates="thread",
        cascade="all, delete-orphan",
        foreign_keys="[PublicChatSnapshot.thread_id]",
    )
    token_usages = relationship(
        "TokenUsage",
        back_populates="thread",
        cascade="all, delete-orphan",
    )


class NewChatMessage(BaseModel, TimestampMixin):
    """
    Message model for the new chat feature.
    Stores individual messages in assistant-ui format.
    """

    __tablename__ = "new_chat_messages"

    # Partial unique index on (thread_id, turn_id, role) where turn_id IS NOT NULL.
    # Mirrors alembic migration 141. Lets the streaming agent and the
    # legacy frontend appendMessage call coexist idempotently — the second
    # writer trips the unique and recovers without creating a duplicate row.
    # Partial so legacy NULL turn_id rows and clone/snapshot inserts in
    # app/services/public_chat_service.py (which omit turn_id) are unaffected.
    __table_args__ = (
        Index(
            "uq_new_chat_messages_thread_turn_role",
            "thread_id",
            "turn_id",
            "role",
            unique=True,
            postgresql_where=text("turn_id IS NOT NULL"),
        ),
    )

    role = Column(SQLAlchemyEnum(NewChatMessageRole), nullable=False)
    # Content stored as JSONB to support rich content (text, tool calls, etc.)
    content = Column(JSONB, nullable=False)

    # Foreign key to thread
    thread_id = Column(
        Integer,
        ForeignKey("new_chat_threads.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )

    # Track who sent this message (for shared chats)
    author_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )

    # Per-turn correlation id sourced from ``configurable.turn_id`` at
    # streaming time (``f"{chat_id}:{ms}"``). Nullable because legacy rows
    # predate the column. Used by C1's edit-from-arbitrary-position to map
    # a message back to the LangGraph checkpoint that produced its turn.
    turn_id = Column(String(64), nullable=True, index=True)

    # Mirrors the parent thread source for publication-level filtering.
    # This denormalization avoids join-dependent logical replication rules.
    source = Column(
        Text, nullable=False, default="surfsense", server_default="surfsense"
    )
    platform_metadata = Column(JSONB, nullable=True)

    # Relationships
    thread = relationship("NewChatThread", back_populates="messages")
    author = relationship("User")
    comments = relationship(
        "ChatComment",
        back_populates="message",
        cascade="all, delete-orphan",
    )
    token_usage = relationship(
        "TokenUsage",
        back_populates="message",
        uselist=False,
        cascade="all, delete-orphan",
    )


class TokenUsage(BaseModel, TimestampMixin):
    """
    Tracks LLM token consumption per assistant turn.

    One row per usage event. For chat, linked to a specific message via message_id.
    The usage_type column enables future extension to track non-chat usage
    (indexing, image generation, podcasts, etc.) without schema changes.
    """

    __tablename__ = "token_usage"

    # Partial unique index on (message_id) where message_id IS NOT NULL.
    # Mirrors alembic migration 142. Lets the streaming agent's
    # ``finalize_assistant_turn`` and the legacy frontend ``append_message``
    # recovery branch both use ``INSERT ... ON CONFLICT DO NOTHING`` without
    # racing on a SELECT-then-INSERT window. Partial so non-chat usage rows
    # (indexing, image generation, podcasts) — which keep ``message_id`` NULL
    # because there is no per-message anchor — are unaffected.
    __table_args__ = (
        Index(
            "uq_token_usage_message_id",
            "message_id",
            unique=True,
            postgresql_where=text("message_id IS NOT NULL"),
        ),
    )

    prompt_tokens = Column(Integer, nullable=False, default=0)
    completion_tokens = Column(Integer, nullable=False, default=0)
    total_tokens = Column(Integer, nullable=False, default=0)
    cost_micros = Column(BigInteger, nullable=False, default=0, server_default="0")
    model_breakdown = Column(JSONB, nullable=True)
    call_details = Column(JSONB, nullable=True)

    usage_type = Column(String(50), nullable=False, default="chat", index=True)

    thread_id = Column(
        Integer,
        ForeignKey("new_chat_threads.id", ondelete="CASCADE"),
        nullable=True,
        index=True,
    )
    message_id = Column(
        Integer,
        ForeignKey("new_chat_messages.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    user_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )

    # Relationships
    thread = relationship("NewChatThread", back_populates="token_usages")
    message = relationship("NewChatMessage", back_populates="token_usage")
    workspace = relationship("Workspace")
    user = relationship("User")


class PublicChatSnapshot(BaseModel, TimestampMixin):
    """
    Immutable snapshot of a chat thread for public sharing.

    Each snapshot is a frozen copy of the chat at a specific point in time.
    The snapshot_data JSONB contains all messages and metadata needed to
    render the public chat without querying the original thread.
    """

    __tablename__ = "public_chat_snapshots"

    # Link to original thread - CASCADE DELETE when thread is deleted.
    # Null for a shared notebook output (c0010): exactly one of the two is set.
    thread_id = Column(
        Integer,
        ForeignKey("new_chat_threads.id", ondelete="CASCADE"),
        nullable=True,
        index=True,
    )
    notebook_output_id = Column(
        Integer,
        ForeignKey("notebook_outputs.id", ondelete="CASCADE"),
        nullable=True,
        index=True,
    )
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    # After this the link answers "expired"; null never expires.
    expires_at = Column(TIMESTAMP(timezone=True), nullable=True)

    # Public access token (unique URL identifier)
    share_token = Column(
        String(64),
        nullable=False,
        unique=True,
        index=True,
    )

    content_hash = Column(
        String(64),
        nullable=False,
        index=True,
    )

    snapshot_data = Column(JSONB, nullable=False)

    message_ids = Column(ARRAY(Integer), nullable=False)

    created_by_user_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )

    # Relationships
    thread = relationship(
        "NewChatThread",
        back_populates="snapshots",
        foreign_keys="[PublicChatSnapshot.thread_id]",
    )
    created_by = relationship("User")

    # Constraints
    __table_args__ = (
        # Prevent duplicate snapshots of the same content for the same thread
        UniqueConstraint(
            "thread_id", "content_hash", name="uq_snapshot_thread_content_hash"
        ),
        CheckConstraint(
            "num_nonnulls(thread_id, notebook_output_id) = 1",
            name="ck_public_snapshot_one_subject",
        ),
    )


class ChatComment(BaseModel, TimestampMixin):
    """
    Comment model for comments on AI chat responses.
    Supports one level of nesting (replies to comments, but no replies to replies).
    """

    __tablename__ = "chat_comments"

    message_id = Column(
        Integer,
        ForeignKey("new_chat_messages.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    # Denormalized thread_id for efficient Zero subscriptions (one per thread)
    thread_id = Column(
        Integer,
        ForeignKey("new_chat_threads.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    parent_id = Column(
        Integer,
        ForeignKey("chat_comments.id", ondelete="CASCADE"),
        nullable=True,
        index=True,
    )
    author_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    content = Column(Text, nullable=False)
    updated_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=lambda: datetime.now(UTC),
        onupdate=lambda: datetime.now(UTC),
        index=True,
    )

    # Relationships
    message = relationship("NewChatMessage", back_populates="comments")
    thread = relationship("NewChatThread")
    author = relationship("User")
    parent = relationship(
        "ChatComment", remote_side="ChatComment.id", backref="replies"
    )
    mentions = relationship(
        "ChatCommentMention",
        back_populates="comment",
        cascade="all, delete-orphan",
    )


class ChatCommentMention(BaseModel, TimestampMixin):
    """
    Tracks @mentions in chat comments for notification purposes.
    """

    __tablename__ = "chat_comment_mentions"

    comment_id = Column(
        Integer,
        ForeignKey("chat_comments.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    mentioned_user_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )

    # Relationships
    comment = relationship("ChatComment", back_populates="mentions")
    mentioned_user = relationship("User")


class ChatSessionState(BaseModel):
    """
    Tracks real-time session state for shared chat collaboration.
    One record per thread, synced via Zero.
    """

    __tablename__ = "chat_session_state"

    thread_id = Column(
        Integer,
        ForeignKey("new_chat_threads.id", ondelete="CASCADE"),
        nullable=False,
        unique=True,
        index=True,
    )
    ai_responding_to_user_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    updated_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=lambda: datetime.now(UTC),
        onupdate=lambda: datetime.now(UTC),
    )

    thread = relationship("NewChatThread")
    ai_responding_to_user = relationship("User")


class Folder(BaseModel, TimestampMixin):
    __tablename__ = "folders"

    name = Column(String(255), nullable=False, index=True)
    position = Column(String(50), nullable=False, index=True)
    parent_id = Column(
        Integer,
        ForeignKey("folders.id", ondelete="CASCADE"),
        nullable=True,
        index=True,
    )
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    created_by_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    updated_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=lambda: datetime.now(UTC),
        onupdate=lambda: datetime.now(UTC),
        index=True,
    )
    folder_metadata = Column("metadata", JSONB, nullable=True)

    parent = relationship("Folder", remote_side="Folder.id", backref="children")
    workspace = relationship("Workspace", back_populates="folders")
    created_by = relationship("User", back_populates="folders")
    documents = relationship("Document", back_populates="folder", passive_deletes=True)


class Document(BaseModel, TimestampMixin):
    __tablename__ = "documents"

    title = Column(String, nullable=False, index=True)
    document_type = Column(SQLAlchemyEnum(DocumentType), nullable=False)
    document_metadata = Column(JSON, nullable=True)

    content = Column(Text, nullable=False)
    # ``content_hash`` is intentionally NOT globally unique. In a real
    # filesystem two files at different paths can hold identical bytes,
    # and the agent's ``write_file`` flow needs that semantic to support
    # copy / duplicate operations. Path uniqueness lives on
    # ``unique_identifier_hash`` (per workspace). The hash remains
    # indexed because connector indexers consult it as a change-detection
    # / cross-source dedup hint via :func:`check_duplicate_document`.
    # See migration 133.
    content_hash = Column(String, nullable=False, index=True)
    unique_identifier_hash = Column(String, nullable=True, index=True, unique=True)

    # Virtual path the content lives at (``/documents/...``): the authored-once
    # identity resolution keys on. Nullable, healed on write; mirrors
    # ``document_metadata['virtual_path']`` until the cut. Index in migration 177.
    path = Column(String, nullable=True)

    embedding = Column(Vector(config.EMBEDDING_DIMENSION))

    # BlockNote live editing state (NULL when never edited)
    # DEPRECATED: Will be removed in a future migration. Use source_markdown instead.
    blocknote_document = Column(JSONB, nullable=True)

    # Full raw markdown content for the Plate.js editor.
    # This is the source of truth for document content in the editor.
    # Populated from markdown at ingestion time, or from blocknote_document migration.
    source_markdown = Column(Text, nullable=True)

    # Background reindex flag (set when editor content is saved)
    content_needs_reindexing = Column(
        Boolean, nullable=False, default=False, server_default=text("false")
    )

    # Track when document was last updated by indexers, processors, or editor
    updated_at = Column(TIMESTAMP(timezone=True), nullable=True, index=True)

    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
    )

    folder_id = Column(
        Integer,
        ForeignKey("folders.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )

    # Track who created/uploaded this document
    created_by_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="SET NULL"),
        nullable=True,  # Nullable for backward compatibility with existing records
        index=True,
    )

    # Track which connector created this document (for cleanup on connector deletion)
    connector_id = Column(
        Integer,
        ForeignKey("search_source_connectors.id", ondelete="SET NULL"),
        nullable=True,  # Nullable for manually uploaded docs without connector
        index=True,
    )

    # Processing status for real-time visibility (JSONB)
    # Format: {"state": "ready"} or {"state": "processing"} or {"state": "failed", "reason": "..."}
    # Default to {"state": "ready"} for backward compatibility with existing documents
    status = Column(
        JSONB,
        nullable=False,
        default=DocumentStatus.ready,
        server_default=text('\'{"state": "ready"}\'::jsonb'),
        index=True,
    )

    # Relationships
    workspace = relationship("Workspace", back_populates="documents")
    folder = relationship("Folder", back_populates="documents")
    created_by = relationship("User", back_populates="documents")
    connector = relationship("SearchSourceConnector", back_populates="documents")
    chunks = relationship(
        "Chunk",
        back_populates="document",
        cascade="all, delete-orphan",
        order_by="Chunk.position",
    )
    # Original upload + future derived artifacts (redacted, filled-form).
    # Model lives in app.file_storage.persistence to keep that feature cohesive.
    files = relationship(
        "DocumentFile", back_populates="document", cascade="all, delete-orphan"
    )
    artifact = relationship(
        "Artifact",
        back_populates="document",
        cascade="all, delete-orphan",
        passive_deletes=True,
        uselist=False,
    )


class DocumentVersion(BaseModel, TimestampMixin):
    __tablename__ = "document_versions"
    __table_args__ = (
        UniqueConstraint("document_id", "version_number", name="uq_document_version"),
    )

    document_id = Column(
        Integer,
        ForeignKey("documents.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    version_number = Column(Integer, nullable=False)
    source_markdown = Column(Text, nullable=True)
    content_hash = Column(String, nullable=False)
    title = Column(String, nullable=True)

    document = relationship(
        "Document", backref=backref("versions", passive_deletes=True)
    )


class Chunk(BaseModel, TimestampMixin):
    __tablename__ = "chunks"

    content = Column(Text, nullable=False)
    embedding = Column(Vector(config.EMBEDDING_DIMENSION))
    # Explicit document order; ids don't follow it since incremental
    # re-indexing keeps unchanged rows across edits. Deliberately not indexed:
    # ordering reads are document-scoped (covered by ix_chunks_document_id) and
    # building a position index on the large chunks table is not worth it.
    position = Column(Integer, nullable=False, server_default="0")

    # 1-based inclusive line range this chunk was cut from, in the document's
    # source markdown. Sole consumer is rendering true line numbers on search
    # excerpts; never a stored reference, so a rebuild cannot strand it.
    # NULL on rows written before spans existed.
    start_line = Column(Integer, nullable=True)
    end_line = Column(Integer, nullable=True)

    # 1-based inclusive pages of the original PDF this chunk's text was read
    # from, so a citation opens the original at the right page. NULL when the
    # page is unknown (not a PDF, or not yet located; see app.source_pages).
    page_start = Column(Integer, nullable=True)
    page_end = Column(Integer, nullable=True)

    document_id = Column(
        Integer,
        ForeignKey("documents.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    document = relationship("Document", back_populates="chunks")


class VideoPresentationRun(BaseModel, TimestampMixin):
    """Lifecycle record for one video-presentation generation.

    The delivered result — Remotion slides, scene codes, narration — lives in
    the Artifact pointed to by ``artifact_id``. This row tracks only the run:
    its status, and the reason a failed one failed.
    """

    __tablename__ = "video_presentation_runs"

    title = Column(String(500), nullable=False)
    status = Column(
        SQLAlchemyEnum(
            VideoPresentationStatus,
            name="video_presentation_status",
            create_type=False,
            values_callable=lambda x: [e.value for e in x],
        ),
        nullable=False,
        default=VideoPresentationStatus.READY,
        server_default="ready",
        index=True,
    )
    error = Column(Text, nullable=True)

    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
    )
    workspace = relationship("Workspace", back_populates="video_presentation_runs")

    thread_id = Column(
        Integer,
        ForeignKey("new_chat_threads.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    thread = relationship("NewChatThread")

    artifact_id = Column(
        Integer,
        ForeignKey("artifacts.id", ondelete="SET NULL"),
        nullable=True,
    )


class DeliverableJob(BaseModel, TimestampMixin):
    """Durable lifecycle for one independently executed deliverable request."""

    __tablename__ = "deliverable_jobs"

    kind = Column(String(64), nullable=False)
    title = Column(String(500), nullable=False)
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    thread_id = Column(
        Integer,
        ForeignKey("new_chat_threads.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    created_by_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="SET NULL"),
        nullable=True,
    )
    # TEXT: Gemini tool-call ids carry a thought signature past 255 chars.
    tool_call_id = Column(Text, nullable=False)
    request = Column(JSONB, nullable=False, default=dict, server_default="{}")
    checkpoint = Column(JSONB, nullable=False, default=dict, server_default="{}")
    status = Column(
        SQLAlchemyEnum(
            DeliverableJobStatus,
            name="deliverable_job_status",
            create_type=False,
            values_callable=lambda enum: [item.value for item in enum],
        ),
        nullable=False,
        default=DeliverableJobStatus.QUEUED,
        server_default=DeliverableJobStatus.QUEUED.value,
    )
    phase = Column(String(64), nullable=True)
    progress = Column(Integer, nullable=False, default=0, server_default="0")
    artifact_id = Column(
        Integer,
        ForeignKey("artifacts.id", ondelete="SET NULL"),
        nullable=True,
    )
    celery_task_id = Column(String(255), nullable=True)
    attempt_count = Column(Integer, nullable=False, default=1, server_default="1")
    failure_code = Column(String(64), nullable=True)
    internal_error = Column(String(2000), nullable=True)
    cancel_requested_at = Column(TIMESTAMP(timezone=True), nullable=True)
    claimed_at = Column(TIMESTAMP(timezone=True), nullable=True)
    heartbeat_at = Column(TIMESTAMP(timezone=True), nullable=True)
    finished_at = Column(TIMESTAMP(timezone=True), nullable=True)
    updated_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=lambda: datetime.now(UTC),
        onupdate=lambda: datetime.now(UTC),
        server_default=text("NOW()"),
        index=True,
    )

    __table_args__ = (
        # Keyed on md5 because a long id can outgrow a btree entry.
        Index(
            "uq_deliverable_jobs_workspace_kind_tool_call",
            "workspace_id",
            "kind",
            func.md5(tool_call_id),
            unique=True,
        ),
        CheckConstraint(
            "progress >= 0 AND progress <= 100",
            name="ck_deliverable_jobs_progress",
        ),
        CheckConstraint(
            "attempt_count >= 1",
            name="ck_deliverable_jobs_attempt_count",
        ),
        Index(
            "ix_deliverable_jobs_workspace_status",
            "workspace_id",
            "status",
        ),
        Index(
            "ix_deliverable_jobs_outbox",
            "updated_at",
            postgresql_where=text("status = 'queued'"),
        ),
        Index(
            "ix_deliverable_jobs_stale_running",
            "heartbeat_at",
            "id",
            postgresql_where=text("status IN ('running', 'cancelling')"),
        ),
    )


class Connection(BaseModel, TimestampMixin):
    __tablename__ = "connections"

    provider = Column(String(100), nullable=False, index=True)
    base_url = Column(String(500), nullable=True)
    api_key = Column(String, nullable=True)
    extra = Column(JSONB, nullable=False, default=dict, server_default="{}")
    scope = Column(SQLAlchemyEnum(ConnectionScope), nullable=False, index=True)
    enabled = Column(Boolean, nullable=False, default=True, server_default="true")

    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=True,
    )
    user_id = Column(
        UUID(as_uuid=True), ForeignKey("user.id", ondelete="CASCADE"), nullable=True
    )

    workspace = relationship("Workspace", back_populates="connections")
    user = relationship("User", back_populates="connections")
    models = relationship(
        "Model",
        back_populates="connection",
        order_by="Model.id",
        cascade="all, delete-orphan",
        passive_deletes=True,
    )

    __table_args__ = (
        CheckConstraint(
            "(scope = 'GLOBAL' AND workspace_id IS NULL AND user_id IS NULL) OR "
            "(scope = 'SEARCH_SPACE' AND workspace_id IS NOT NULL AND user_id IS NOT NULL) OR "
            "(scope = 'USER' AND user_id IS NOT NULL)",
            name="ck_connections_scope_owner",
        ),
    )


class Model(BaseModel, TimestampMixin):
    __tablename__ = "models"

    connection_id = Column(
        Integer,
        ForeignKey("connections.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    model_id = Column(String(255), nullable=False)
    display_name = Column(String(255), nullable=True)
    source = Column(
        SQLAlchemyEnum(ModelSource),
        nullable=False,
        default=ModelSource.DISCOVERED,
        server_default=ModelSource.DISCOVERED.value,
    )
    supports_chat = Column(Boolean, nullable=True)
    max_input_tokens = Column(Integer, nullable=True)
    supports_image_input = Column(Boolean, nullable=True)
    supports_tools = Column(Boolean, nullable=True)
    supports_image_generation = Column(Boolean, nullable=True)
    capabilities_override = Column(
        JSONB, nullable=False, default=dict, server_default="{}"
    )
    enabled = Column(Boolean, nullable=False, default=True, server_default="true")
    billing_tier = Column(String(50), nullable=True, index=True)
    catalog = Column(JSONB, nullable=False, default=dict, server_default="{}")

    connection = relationship("Connection", back_populates="models")

    __table_args__ = (
        UniqueConstraint(
            "connection_id", "model_id", name="uq_models_connection_model_id"
        ),
        Index("ix_models_model_id", "model_id"),
    )


class Workspace(BaseModel, TimestampMixin):
    __tablename__ = "workspaces"

    name = Column(String(100), nullable=False, index=True)
    description = Column(String(500), nullable=True)

    citations_enabled = Column(
        Boolean, nullable=False, default=True
    )  # Enable/disable citations
    api_access_enabled = Column(
        Boolean, nullable=False, default=False, server_default="false"
    )
    qna_custom_instructions = Column(
        Text, nullable=True, default=""
    )  # User's custom instructions

    shared_memory_md = Column(Text, nullable=True, server_default="")

    # Connection/model role bindings.
    # Note: ID values preserve the existing convention:
    #   - 0: Auto mode
    #   - Negative IDs: Global virtual models from global_llm_config.yaml
    #   - Positive IDs: User/workspace models from the models table
    chat_model_id = Column(
        Integer, nullable=True, default=0, server_default="0"
    )  # For agent/chat operations, defaults to Auto mode
    image_gen_model_id = Column(
        Integer, nullable=True, default=0, server_default="0"
    )  # For image generation, defaults to Auto mode when eligible
    vision_model_id = Column(
        Integer, nullable=True, default=0, server_default="0"
    )  # For vision/screenshot analysis, defaults to Auto mode

    # First time this workspace went ready via its own model (source=="models").
    # NULL = never self-configured. Set once, never cleared; splits a needs_setup
    # verdict into first-run vs. recovery.
    llm_setup_completed_at = Column(TIMESTAMP(timezone=True), nullable=True)

    # Progressive git-native flip: this workspace uses the git-backed knowledge
    # store only when this AND the global KNOWLEDGE_STORE_ENABLED are true.
    # Flipped per workspace after its migration seed passes parity.
    knowledge_store_enabled = Column(
        Boolean, nullable=False, default=False, server_default="false"
    )

    # Revision the derived index (chunks + embeddings) was last built from.
    # NULL means never indexed; ``!= store.get_current_revision()`` is the drift
    # predicate the sweep uses to re-drive a workspace.
    last_indexed_revision = Column(String(64), nullable=True)

    user_id = Column(
        UUID(as_uuid=True), ForeignKey("user.id", ondelete="CASCADE"), nullable=False
    )
    user = relationship("User", back_populates="workspaces")

    folders = relationship(
        "Folder",
        back_populates="workspace",
        order_by="Folder.position",
        cascade="all, delete-orphan",
    )
    documents = relationship(
        "Document",
        back_populates="workspace",
        order_by="Document.id",
        cascade="all, delete-orphan",
    )
    new_chat_threads = relationship(
        "NewChatThread",
        back_populates="workspace",
        order_by="NewChatThread.updated_at.desc()",
        cascade="all, delete-orphan",
    )
    podcasts = relationship(
        "Podcast",
        back_populates="workspace",
        order_by="Podcast.id.desc()",
        cascade="all, delete-orphan",
    )
    video_presentation_runs = relationship(
        "VideoPresentationRun",
        back_populates="workspace",
        order_by="VideoPresentationRun.id.desc()",
        cascade="all, delete-orphan",
    )
    logs = relationship(
        "Log",
        back_populates="workspace",
        order_by="Log.id",
        cascade="all, delete-orphan",
    )
    notifications = relationship(
        "Notification",
        back_populates="workspace",
        order_by="Notification.created_at.desc()",
        cascade="all, delete-orphan",
    )
    search_source_connectors = relationship(
        "SearchSourceConnector",
        back_populates="workspace",
        order_by="SearchSourceConnector.id",
        cascade="all, delete-orphan",
    )
    connections = relationship(
        "Connection",
        back_populates="workspace",
        order_by="Connection.id",
        cascade="all, delete-orphan",
        passive_deletes=True,
    )

    automations = relationship(
        "Automation",
        back_populates="workspace",
        order_by="Automation.id",
        cascade="all, delete-orphan",
        passive_deletes=True,
    )

    # RBAC relationships
    roles = relationship(
        "WorkspaceRole",
        back_populates="workspace",
        order_by="WorkspaceRole.id",
        cascade="all, delete-orphan",
    )
    memberships = relationship(
        "WorkspaceMembership",
        back_populates="workspace",
        order_by="WorkspaceMembership.id",
        cascade="all, delete-orphan",
    )
    invites = relationship(
        "WorkspaceInvite",
        back_populates="workspace",
        order_by="WorkspaceInvite.id",
        cascade="all, delete-orphan",
    )


class SearchSourceConnector(BaseModel, TimestampMixin):
    """A connection, owned by an account (``user_id``), not a workspace.

    ``workspace_id`` is the scoping key ONLY for ``MCP_CONNECTOR`` rows (a
    workspace's custom API tools — untouched by the account-connections
    change, see ``custom/ACCOUNT_CONNECTIONS.md``), where it stays NOT NULL
    in practice and is what ``uq_workspace_user_connector_type_name`` below
    enforces. For every other connector type ``workspace_id`` is NULL: the
    row belongs to the account, and which workspaces have it switched on is
    ``SearchSourceConnectorEnablement``, not this column.
    """

    __tablename__ = "search_source_connectors"
    __table_args__ = (
        UniqueConstraint(
            "workspace_id",
            "user_id",
            "connector_type",
            "name",
            name="uq_workspace_user_connector_type_name",
        ),
        Index(
            "uq_account_connector_type_name",
            "user_id",
            "connector_type",
            "name",
            unique=True,
            postgresql_where=text("workspace_id IS NULL"),
        ),
    )

    name = Column(String(100), nullable=False, index=True)
    connector_type = Column(SQLAlchemyEnum(SearchSourceConnectorType), nullable=False)
    is_indexable = Column(Boolean, nullable=False, default=False)
    last_indexed_at = Column(TIMESTAMP(timezone=True), nullable=True)
    config = Column(JSON, nullable=False)

    # Vision LLM for image files - disabled by default to save cost/time.
    # When enabled, images are described via a vision language model instead
    # of falling back to the document parser.
    enable_vision_llm = Column(
        Boolean, nullable=False, default=False, server_default="false"
    )

    # Periodic indexing fields. Live data only for MCP_CONNECTOR (which
    # never populates them). Every other connector type's periodic settings
    # moved to the per-workspace SearchSourceConnectorEnablement row.
    periodic_indexing_enabled = Column(Boolean, nullable=False, default=False)
    indexing_frequency_minutes = Column(Integer, nullable=True)
    next_scheduled_at = Column(TIMESTAMP(timezone=True), nullable=True)

    # NULL for every connector type except MCP_CONNECTOR (see class docstring).
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=True,
    )
    workspace = relationship("Workspace", back_populates="search_source_connectors")

    user_id = Column(
        UUID(as_uuid=True), ForeignKey("user.id", ondelete="CASCADE"), nullable=False
    )
    user = relationship("User", back_populates="search_source_connectors")

    # Documents created by this connector (for cleanup on connector deletion)
    documents = relationship("Document", back_populates="connector")

    # Per-workspace enable switches (empty for MCP_CONNECTOR rows).
    enablements = relationship(
        "SearchSourceConnectorEnablement",
        back_populates="connector",
        cascade="all, delete-orphan",
    )


class SearchSourceConnectorEnablement(BaseModel, TimestampMixin):
    """One workspace's override for one account connection.

    Default is ON: a connection is usable in every workspace its owner
    belongs to with no row here at all. A row exists only to record an
    explicit override — ``enabled=False`` (the owner switched it off in
    this one workspace) or workspace-specific indexing settings (selected
    folders, periodic schedule) while staying enabled. See
    ``app/services/connector_enablement.py`` for the read side.

    Deleting the workspace cascades here only; the connector (credentials
    included) is untouched. Deleting the connector cascades here too (every
    workspace loses it at once — the "disconnect my account" action).
    """

    __tablename__ = "search_source_connector_enablements"
    __table_args__ = (
        UniqueConstraint(
            "connector_id",
            "workspace_id",
            name="uq_connector_workspace_enablement",
        ),
    )

    connector_id = Column(
        Integer,
        ForeignKey("search_source_connectors.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    connector = relationship("SearchSourceConnector", back_populates="enablements")

    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    workspace = relationship("Workspace")

    # Audit only ("who switched this on here") — never an access grant.
    # A shared workspace's other members still never see this account's
    # credentials; see "Security in shared workspaces" in the design doc.
    enabled_by_user_id = Column(
        UUID(as_uuid=True), ForeignKey("user.id", ondelete="SET NULL"), nullable=True
    )

    # True (the default) means no override: the connection is on here
    # because its owner belongs to the workspace, same as having no row.
    # False is the only reason a row is created purely to disable.
    enabled = Column(Boolean, nullable=False, default=True, server_default="true")

    is_indexable = Column(Boolean, nullable=False, default=False)
    last_indexed_at = Column(TIMESTAMP(timezone=True), nullable=True)

    periodic_indexing_enabled = Column(Boolean, nullable=False, default=False)
    indexing_frequency_minutes = Column(Integer, nullable=True)
    next_scheduled_at = Column(TIMESTAMP(timezone=True), nullable=True)

    # Per-workspace knobs only (selected_folders, selected_files,
    # indexing_options for Google Drive). Never credentials — those stay on
    # the connector's own ``config``, shared read-only across workspaces.
    indexing_config = Column(JSON, nullable=True)


class Log(BaseModel, TimestampMixin):
    __tablename__ = "logs"

    level = Column(SQLAlchemyEnum(LogLevel), nullable=False, index=True)
    status = Column(SQLAlchemyEnum(LogStatus), nullable=False, index=True)
    message = Column(Text, nullable=False)
    source = Column(
        String(200), nullable=True, index=True
    )  # Service/component that generated the log
    log_metadata = Column(JSON, nullable=True, default={})  # Additional context data

    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
    )
    workspace = relationship("Workspace", back_populates="logs")


class WorkspaceRole(BaseModel, TimestampMixin):
    """
    Custom roles that can be defined per workspace.
    Each workspace can have multiple roles with different permission sets.
    """

    __tablename__ = "workspace_roles"
    __table_args__ = (
        UniqueConstraint(
            "workspace_id",
            "name",
            name="uq_workspace_role_name",
        ),
    )

    name = Column(String(100), nullable=False, index=True)
    description = Column(String(500), nullable=True)
    # List of Permission enum values (e.g., ["documents:read", "chats:create"])
    permissions = Column(ARRAY(String), nullable=False, default=[])
    # Whether this role is assigned to new members by default when they join via invite
    is_default = Column(Boolean, nullable=False, default=False)
    # System roles (Owner, Editor, Viewer) cannot be deleted
    is_system_role = Column(Boolean, nullable=False, default=False)

    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
    )
    workspace = relationship("Workspace", back_populates="roles")

    memberships = relationship(
        "WorkspaceMembership", back_populates="role", passive_deletes=True
    )
    invites = relationship(
        "WorkspaceInvite", back_populates="role", passive_deletes=True
    )


class WorkspaceMembership(BaseModel, TimestampMixin):
    """
    Tracks user membership in workspaces with their assigned role.
    Each user can be a member of multiple workspaces with different roles.
    """

    __tablename__ = "workspace_memberships"
    __table_args__ = (
        UniqueConstraint(
            "user_id",
            "workspace_id",
            name="uq_user_workspace_membership",
        ),
    )

    user_id = Column(
        UUID(as_uuid=True), ForeignKey("user.id", ondelete="CASCADE"), nullable=False
    )
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
    )
    role_id = Column(
        Integer,
        ForeignKey("workspace_roles.id", ondelete="SET NULL"),
        nullable=True,
    )
    # Indicates if this user is the original creator/owner of the workspace
    is_owner = Column(Boolean, nullable=False, default=False)
    # Timestamp when the user joined (via invite or as creator)
    joined_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=lambda: datetime.now(UTC),
    )
    # Reference to the invite used to join (null if owner/creator)
    invited_by_invite_id = Column(
        Integer,
        ForeignKey("workspace_invites.id", ondelete="SET NULL"),
        nullable=True,
    )
    user = relationship("User", back_populates="workspace_memberships")
    workspace = relationship("Workspace", back_populates="memberships")
    role = relationship("WorkspaceRole", back_populates="memberships")
    invited_by_invite = relationship(
        "WorkspaceInvite", back_populates="used_by_memberships"
    )


class WorkspaceInvite(BaseModel, TimestampMixin):
    """
    Invite links for workspaces.
    Users can create invite links with specific roles that others can use to join.
    """

    __tablename__ = "workspace_invites"

    # Unique invite code (used in invite URLs)
    invite_code = Column(String(64), nullable=False, unique=True, index=True)
    # The registered account this invite is for (c0010); only that account can
    # accept it. Null only on legacy link invites, which the migration retired.
    email = Column(String(320), nullable=True, index=True)
    # When the invitation email last went out, for the resend cooldown.
    last_sent_at = Column(TIMESTAMP(timezone=True), nullable=True)

    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
    )
    # Role to assign when invite is used (null means use default role)
    role_id = Column(
        Integer,
        ForeignKey("workspace_roles.id", ondelete="SET NULL"),
        nullable=True,
    )
    # User who created this invite
    created_by_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="SET NULL"),
        nullable=True,
    )

    # Expiration timestamp (null means never expires)
    expires_at = Column(TIMESTAMP(timezone=True), nullable=True)
    # Maximum number of times this invite can be used (null means unlimited)
    max_uses = Column(Integer, nullable=True)
    # Number of times this invite has been used
    uses_count = Column(Integer, nullable=False, default=0)
    # Whether this invite is currently active
    is_active = Column(Boolean, nullable=False, default=True)
    # Optional custom name/label for the invite
    name = Column(String(100), nullable=True)

    workspace = relationship("Workspace", back_populates="invites")
    role = relationship("WorkspaceRole", back_populates="invites")
    created_by = relationship("User", back_populates="created_invites")
    used_by_memberships = relationship(
        "WorkspaceMembership",
        back_populates="invited_by_invite",
        passive_deletes=True,
    )


class PromptMode(StrEnum):
    transform = "transform"
    explore = "explore"


class Prompt(BaseModel, TimestampMixin):
    __tablename__ = "prompts"
    __table_args__ = (
        UniqueConstraint(
            "user_id",
            "default_prompt_slug",
            name="uq_prompt_user_default_slug",
        ),
    )

    user_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=True,
        index=True,
    )
    default_prompt_slug = Column(String(100), nullable=True, index=True)
    name = Column(String(200), nullable=False)
    prompt = Column(Text, nullable=False)
    mode = Column(
        SQLAlchemyEnum(PromptMode, name="prompt_mode", create_type=False),
        nullable=False,
    )
    version = Column(Integer, nullable=False, default=1)
    is_public = Column(Boolean, nullable=False, default=False)

    user = relationship("User")
    workspace = relationship("Workspace")


if config.AUTH_TYPE == "GOOGLE":

    class OAuthAccount(SQLAlchemyBaseOAuthAccountTableUUID, Base):
        pass

    class User(SQLAlchemyBaseUserTableUUID, Base):
        oauth_accounts: Mapped[list[OAuthAccount]] = relationship(
            "OAuthAccount", lazy="joined"
        )
        workspaces = relationship("Workspace", back_populates="user")
        notifications = relationship(
            "Notification",
            back_populates="user",
            order_by="Notification.created_at.desc()",
            cascade="all, delete-orphan",
        )

        # RBAC relationships
        workspace_memberships = relationship(
            "WorkspaceMembership",
            back_populates="user",
            cascade="all, delete-orphan",
        )
        created_invites = relationship(
            "WorkspaceInvite",
            back_populates="created_by",
            passive_deletes=True,
        )

        # Chat threads created by this user
        new_chat_threads = relationship(
            "NewChatThread",
            back_populates="created_by",
            passive_deletes=True,
        )

        # Documents created/uploaded by this user
        documents = relationship(
            "Document",
            back_populates="created_by",
            passive_deletes=True,
        )

        # Folders created by this user
        folders = relationship(
            "Folder",
            back_populates="created_by",
            passive_deletes=True,
        )

        # Connectors created by this user
        search_source_connectors = relationship(
            "SearchSourceConnector",
            back_populates="user",
            passive_deletes=True,
        )

        connections = relationship(
            "Connection",
            back_populates="user",
            passive_deletes=True,
        )

        # Automations created by this user
        automations = relationship(
            "Automation",
            back_populates="created_by",
            passive_deletes=True,
        )

        # Unified credit wallet (USD micro-units, 1_000_000 == $1.00).
        # Decreases on use (ETL pages + premium model calls), increases on
        # purchase or admin grant. May dip slightly negative
        # when an actual cost exceeds its pre-charge estimate; UI clamps at $0.
        # Starts empty: app.signup_credit grants it once per person, so that a
        # default here cannot mint another $5 for every new account.
        credit_micros_balance = Column(
            BigInteger,
            nullable=False,
            default=0,
            server_default="0",
        )
        # In-flight reservation holds (released/settled at finalize).
        credit_micros_reserved = Column(
            BigInteger, nullable=False, default=0, server_default="0"
        )

        # The Stripe customer billing (app/billing/stripe_billing) charges.
        stripe_customer_id = Column(String, nullable=True)

        # User profile from OAuth
        display_name = Column(String, nullable=True)
        avatar_url = Column(String, nullable=True)

        last_login = Column(TIMESTAMP(timezone=True), nullable=True)

        memory_md = Column(Text, nullable=True, server_default="")

        # Refresh tokens for this user
        refresh_tokens = relationship(
            "RefreshToken",
            back_populates="user",
            cascade="all, delete-orphan",
        )
        personal_access_tokens = relationship(
            "PersonalAccessToken",
            back_populates="user",
            cascade="all, delete-orphan",
        )

else:

    class User(SQLAlchemyBaseUserTableUUID, Base):
        workspaces = relationship("Workspace", back_populates="user")
        notifications = relationship(
            "Notification",
            back_populates="user",
            order_by="Notification.created_at.desc()",
            cascade="all, delete-orphan",
        )

        # RBAC relationships
        workspace_memberships = relationship(
            "WorkspaceMembership",
            back_populates="user",
            cascade="all, delete-orphan",
        )
        created_invites = relationship(
            "WorkspaceInvite",
            back_populates="created_by",
            passive_deletes=True,
        )

        # Chat threads created by this user
        new_chat_threads = relationship(
            "NewChatThread",
            back_populates="created_by",
            passive_deletes=True,
        )

        # Documents created/uploaded by this user
        documents = relationship(
            "Document",
            back_populates="created_by",
            passive_deletes=True,
        )

        # Folders created by this user
        folders = relationship(
            "Folder",
            back_populates="created_by",
            passive_deletes=True,
        )

        # Connectors created by this user
        search_source_connectors = relationship(
            "SearchSourceConnector",
            back_populates="user",
            passive_deletes=True,
        )

        connections = relationship(
            "Connection",
            back_populates="user",
            passive_deletes=True,
        )

        # Automations created by this user
        automations = relationship(
            "Automation",
            back_populates="created_by",
            passive_deletes=True,
        )

        # Unified credit wallet (USD micro-units, 1_000_000 == $1.00).
        # Decreases on use (ETL pages + premium model calls), increases on
        # purchase or admin grant. May dip slightly negative
        # when an actual cost exceeds its pre-charge estimate; UI clamps at $0.
        # Starts empty: app.signup_credit grants it once per person, so that a
        # default here cannot mint another $5 for every new account.
        credit_micros_balance = Column(
            BigInteger,
            nullable=False,
            default=0,
            server_default="0",
        )
        # In-flight reservation holds (released/settled at finalize).
        credit_micros_reserved = Column(
            BigInteger, nullable=False, default=0, server_default="0"
        )

        # The Stripe customer billing (app/billing/stripe_billing) charges.
        stripe_customer_id = Column(String, nullable=True)

        # User profile (can be set manually for non-OAuth users)
        display_name = Column(String, nullable=True)
        avatar_url = Column(String, nullable=True)

        last_login = Column(TIMESTAMP(timezone=True), nullable=True)

        memory_md = Column(Text, nullable=True, server_default="")

        # Refresh tokens for this user
        refresh_tokens = relationship(
            "RefreshToken",
            back_populates="user",
            cascade="all, delete-orphan",
        )
        personal_access_tokens = relationship(
            "PersonalAccessToken",
            back_populates="user",
            cascade="all, delete-orphan",
        )


class AgentActionLog(BaseModel):
    """Append-only audit trail of every tool call dispatched by the agent.

    One row per ``ToolMessage`` produced; written by ``ActionLogMiddleware``
    in its ``aafter_tool`` hook. Rows are referenced by the
    ``/api/threads/{thread_id}/revert/{action_id}`` route to look up an
    action's stored ``reverse_descriptor`` and replay it.

    The table is intentionally narrow: large tool outputs are NOT stored
    here. Result text lives in the langgraph checkpoint; this row only
    keeps a short ``result_id`` (the LangChain ``ToolMessage.id`` or a
    spilled-content path) for correlation.
    """

    __tablename__ = "agent_action_log"

    thread_id = Column(
        Integer,
        ForeignKey("new_chat_threads.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    user_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    # ``turn_id`` historically held the LangChain ``tool_call.id``. It has
    # been renamed to ``tool_call_id`` (with a parallel column kept for one
    # release for back-compat). The real chat-turn id lives in
    # ``chat_turn_id`` and is sourced from ``configurable.turn_id``.
    # TEXT with hash indexes: Gemini tool-call ids carry a thought signature
    # far past 64 chars, and past what a btree entry can hold.
    turn_id = Column(Text, nullable=True)
    tool_call_id = Column(Text, nullable=True)
    chat_turn_id = Column(String(64), nullable=True, index=True)
    # TEXT with a hash index for the same reason as turn_id/tool_call_id
    # above: ActionLogMiddleware._resolve_message_id reuses the tool-call id,
    # which a Gemini thought signature can push well past a btree's ~2.7 kB
    # entry cap. See c0018_action_log_message_id_text.
    message_id = Column(Text, nullable=True)
    tool_name = Column(String(255), nullable=False, index=True)
    args = Column(JSONB, nullable=True)
    result_id = Column(String(255), nullable=True)
    reversible = Column(
        Boolean, nullable=False, default=False, server_default=text("false")
    )
    reverse_descriptor = Column(JSONB, nullable=True)
    error = Column(JSONB, nullable=True)
    reverse_of = Column(
        Integer,
        ForeignKey("agent_action_log.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    created_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=lambda: datetime.now(UTC),
        server_default=text("(now() AT TIME ZONE 'utc')"),
        index=True,
    )

    __table_args__ = (
        Index("ix_agent_action_log_thread_created", "thread_id", "created_at"),
        Index("ix_agent_action_log_turn_id", "turn_id", postgresql_using="hash"),
        Index(
            "ix_agent_action_log_tool_call_id",
            "tool_call_id",
            postgresql_using="hash",
        ),
        Index(
            "ix_agent_action_log_message_id",
            "message_id",
            postgresql_using="hash",
        ),
        # Partial unique index enforces "at most one revert per
        # original action". Created in migration 137 with
        # ``WHERE reverse_of IS NOT NULL`` so non-revert rows
        # (the vast majority) are unaffected and NULLs don't collide.
        Index(
            "ux_agent_action_log_reverse_of",
            "reverse_of",
            unique=True,
            postgresql_where=text("reverse_of IS NOT NULL"),
        ),
    )


class DocumentRevision(BaseModel):
    """Snapshot of a :class:`Document` row taken before a mutating tool call.

    Written by :class:`KnowledgeBasePersistenceMiddleware` (or its safety-net
    `commit_staged_filesystem_state`) ahead of any NOTE / FILE / EXTENSION
    document write. The row is referenced by ``/revert/{action_id}`` to
    restore the original content in place.
    """

    __tablename__ = "document_revisions"

    # ``ON DELETE SET NULL`` (not CASCADE) so the snapshot survives the
    # hard-delete it describes — without that, ``rm`` would wipe the row
    # we'd need to undo it. See migration ``134_relax_revision_fks``.
    document_id = Column(
        Integer,
        ForeignKey("documents.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    content_before = Column(Text, nullable=True)
    title_before = Column(String, nullable=True)
    folder_id_before = Column(Integer, nullable=True)
    chunks_before = Column(JSONB, nullable=True)
    metadata_before = Column("metadata_before", JSONB, nullable=True)
    # Holds the tool-call id; TEXT + hash index for Gemini's long ids.
    created_by_turn_id = Column(Text, nullable=True)
    agent_action_id = Column(
        Integer,
        ForeignKey("agent_action_log.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    created_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=lambda: datetime.now(UTC),
        server_default=text("(now() AT TIME ZONE 'utc')"),
        index=True,
    )

    __table_args__ = (
        Index(
            "ix_document_revisions_created_by_turn_id",
            "created_by_turn_id",
            postgresql_using="hash",
        ),
    )


class FolderRevision(BaseModel):
    """Snapshot of a :class:`Folder` row taken before a mkdir / move."""

    __tablename__ = "folder_revisions"

    # ``ON DELETE SET NULL`` (not CASCADE) so the snapshot survives the
    # hard-delete it describes — without that, ``rmdir`` would wipe the
    # row we'd need to undo it. See migration ``134_relax_revision_fks``.
    folder_id = Column(
        Integer,
        ForeignKey("folders.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    name_before = Column(String(255), nullable=True)
    parent_id_before = Column(Integer, nullable=True)
    position_before = Column(String(50), nullable=True)
    # Holds the tool-call id; TEXT + hash index for Gemini's long ids.
    created_by_turn_id = Column(Text, nullable=True)
    agent_action_id = Column(
        Integer,
        ForeignKey("agent_action_log.id", ondelete="SET NULL"),
        nullable=True,
        index=True,
    )
    created_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=lambda: datetime.now(UTC),
        server_default=text("(now() AT TIME ZONE 'utc')"),
        index=True,
    )

    __table_args__ = (
        Index(
            "ix_folder_revisions_created_by_turn_id",
            "created_by_turn_id",
            postgresql_using="hash",
        ),
    )


class AgentPermissionRule(BaseModel):
    """Persistent permission rule consumed by :class:`PermissionMiddleware`.

    Scoped at one of: workspace-wide (``user_id`` and ``thread_id`` NULL),
    user-wide (``user_id`` set, ``thread_id`` NULL), or per-thread
    (``thread_id`` set). Loaded at agent build time and converted to
    :class:`Rule` instances inside the agent factory.
    """

    __tablename__ = "agent_permission_rules"

    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    user_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="CASCADE"),
        nullable=True,
        index=True,
    )
    thread_id = Column(
        Integer,
        ForeignKey("new_chat_threads.id", ondelete="CASCADE"),
        nullable=True,
        index=True,
    )
    permission = Column(String(255), nullable=False)
    pattern = Column(String(255), nullable=False, default="*", server_default="*")
    action = Column(String(16), nullable=False)  # allow / deny / ask
    created_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=lambda: datetime.now(UTC),
        server_default=text("(now() AT TIME ZONE 'utc')"),
        index=True,
    )

    __table_args__ = (
        UniqueConstraint(
            "workspace_id",
            "user_id",
            "thread_id",
            "permission",
            "pattern",
            "action",
            name="uq_agent_permission_rules_scope",
        ),
    )


class RefreshToken(Base, TimestampMixin):
    """
    Stores refresh tokens for user session management.
    Each row represents one device/session.
    """

    __tablename__ = "refresh_tokens"
    # As c0024 makes it, so create_all's fresh databases have it too.
    __table_args__ = (
        CheckConstraint("kind IN ('web', 'app')", name="ck_refresh_tokens_kind"),
    )

    id = Column(Integer, primary_key=True, autoincrement=True)
    user_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    user = relationship("User", back_populates="refresh_tokens")
    token_hash = Column(String(64), unique=True, nullable=False, index=True)
    expires_at = Column(TIMESTAMP(timezone=True), nullable=False, index=True)
    revoked_at = Column(TIMESTAMP(timezone=True), nullable=True)
    absolute_expiry = Column(TIMESTAMP(timezone=True), nullable=True)
    family_id = Column(UUID(as_uuid=True), nullable=False, index=True)
    # "web" (a browser or the desktop app: the sliding lifetimes) or "app" (the
    # phone app: one family per device, APP_SESSION_LIFETIME_SECONDS from the
    # sign-in, no idle timeout). app/utils/refresh_tokens.py.
    kind = Column(String(16), nullable=False, default="web", server_default="web")
    # The phone's own name for the signed-in devices list ("iPhone 15 Pro · Soloclef app").
    device_label = Column(String(100), nullable=True)
    # When the family's sign-in began, copied onto every rotated token (c0027),
    # so the devices list keeps it after the daily purge deletes the family's
    # older rows. None on rows made before c0027 (then: the earliest row's).
    signed_in_at = Column(TIMESTAMP(timezone=True), nullable=True)

    @property
    def is_expired(self) -> bool:
        return datetime.now(UTC) >= self.expires_at

    @property
    def is_valid(self) -> bool:
        return not self.is_expired and self.revoked_at is None


class PersonalAccessToken(BaseModel, TimestampMixin):
    """
    Stores hashed Personal Access Tokens for programmatic API access.
    Plaintext tokens are shown once on creation and are never persisted.
    """

    __tablename__ = "personal_access_tokens"

    user_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    user = relationship("User", back_populates="personal_access_tokens")
    token_hash = Column(String(64), unique=True, nullable=False, index=True)
    token_prefix = Column(String(16), nullable=False)
    label = Column(String, nullable=False)
    expires_at = Column(TIMESTAMP(timezone=True), nullable=True, index=True)
    last_used_at = Column(TIMESTAMP(timezone=True), nullable=True)

    @property
    def is_expired(self) -> bool:
        return self.expires_at is not None and datetime.now(UTC) >= self.expires_at

    @property
    def is_valid(self) -> bool:
        return not self.is_expired


class Run(Base, TimestampMixin):
    """One row per scraper-capability invocation, from either the agent door
    or the REST/API-key door.

    Backs the user-facing Scraper-API logs and the agent's tool-boundary
    truncation: the full output lives here while the model sees only a capped
    preview plus this row's id. ``output_text`` is stored as JSONL (one item
    per line, ``exclude_none``) so ``read_run``/``search_run`` can page and grep
    by line without parsing the whole payload. Retained ~30 days via opportunistic
    bounded cleanup on insert.

    ``cost_micros`` ships nullable and unpopulated in this pass; the planned
    per-verb pricing rework will fill it. ``thread_id`` is a free-form string
    (subagent ids look like ``"2099::task:call_x"``), so it is intentionally not
    a foreign key.
    """

    __tablename__ = "runs"
    __allow_unmapped__ = True

    __table_args__ = (Index("ix_runs_workspace_created", "workspace_id", "created_at"),)

    id = Column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    user_id = Column(
        UUID(as_uuid=True),
        ForeignKey("user.id", ondelete="CASCADE"),
        nullable=True,
        index=True,
    )
    thread_id = Column(String(255), nullable=True)
    capability = Column(String(100), nullable=False, index=True)
    origin = Column(String(16), nullable=False)
    status = Column(String(16), nullable=False)
    error = Column(Text, nullable=True)
    input = Column(JSONB, nullable=True)
    output_text = Column(Text, nullable=True)
    item_count = Column(Integer, nullable=False, default=0)
    char_count = Column(Integer, nullable=False, default=0)
    duration_ms = Column(Integer, nullable=True)
    cost_micros = Column(BigInteger, nullable=True)
    # Coarse progress log (list of throttled events) captured during the run;
    # the live fine-grained stream is ephemeral (bus/SSE only).
    progress = Column(JSONB, nullable=True)


class ToolOutputSpill(Base, TimestampMixin):
    """Internal scratch store for main-agent context-editing spills.

    Kept separate from ``runs`` so customer-facing scraper logs stay clean.
    The full ``ToolMessage`` content that context editing evicts is written here
    and the message body is replaced with a ``spill_{id}`` placeholder the agent
    can read back via ``read_run``/``search_run``. Retained ~7 days.
    """

    __tablename__ = "tool_output_spills"
    __allow_unmapped__ = True

    id = Column(UUID(as_uuid=True), primary_key=True, default=uuid.uuid4)
    workspace_id = Column(
        Integer,
        ForeignKey("workspaces.id", ondelete="CASCADE"),
        nullable=True,
        index=True,
    )
    thread_id = Column(String(255), nullable=True)
    tool_name = Column(String(255), nullable=True)
    content = Column(Text, nullable=False)
    char_count = Column(Integer, nullable=False, default=0)


class ModelCompatibility(Base):
    """Per-model verdict from the compatibility sweep.

    Passing our metadata filters does not mean a model can serve a turn through
    this agent harness. ``scripts/sweep_model_compatibility.py`` probes each
    catalogue model and writes the result here; catalogue generation reads the
    ``blocked`` ids back so those models never reach a user. Shared through
    Postgres so all uvicorn workers converge on the same blocklist.
    """

    __tablename__ = "model_compatibility"

    id = Column(Integer, primary_key=True, autoincrement=True)
    model_id = Column(String(255), unique=True, nullable=False, index=True)
    status = Column(String(16), nullable=False, index=True)
    # Which of the three escalating probes broke; NULL when all passed.
    failure_stage = Column(String(32), nullable=True)
    error_code = Column(String(64), nullable=True)
    error_excerpt = Column(Text, nullable=True)
    latency_ms = Column(Integer, nullable=True)
    checked_at = Column(
        TIMESTAMP(timezone=True),
        nullable=False,
        default=lambda: datetime.now(UTC),
        index=True,
    )


# Register model packages that live outside this file so their classes
# are present in Base.metadata before configure_mappers() resolves any
# string-based relationship() references.
from app.artifacts.persistence import (  # noqa: E402, F401
    Artifact,
    ArtifactFile,
)
from app.automations.persistence import (  # noqa: E402, F401
    Automation,
    AutomationRun,
    AutomationTrigger,
)
from app.billing.persistence import (  # noqa: E402, F401
    BillingAccount,
    BillingActionPrice,
    BillingLedgerEntry,
    BillingPack,
    BillingPayment,
    BillingPlan,
    BillingSetting,
    BillingUsage,
    BillingWebhookEvent,
)
from app.etl_pipeline.cache.persistence.models import CachedParse  # noqa: E402, F401
from app.file_storage.persistence import DocumentFile  # noqa: E402, F401
from app.indexing_pipeline.cache.persistence.models import (  # noqa: E402, F401
    CachedEmbeddingSet,
)
from app.knowledge_store.remote.persistence.models import (  # noqa: E402, F401
    WorkspaceGitRemotes,
)
from app.notebooks.earlier_chats import NotebookEarlierChat  # noqa: E402, F401
from app.notebooks.persistence import (  # noqa: E402, F401
    Notebook,
    NotebookNote,
    NotebookOutput,
    NotebookReportTemplate,
    NotebookSource,
    SourceGuide,
)
from app.notifications.persistence import Notification  # noqa: E402, F401
from app.podcasts.persistence import (  # noqa: E402, F401
    Podcast,
    PodcastStatus,
)
from app.public_sharing.notebook_links.models import (  # noqa: E402, F401
    NotebookPublicLink,
)
from app.push.persistence import (  # noqa: E402, F401
    PushDevice,
    PushPreference,
    PushTicket,
)
from app.quick_tasks.model import WorkspaceQuickModel  # noqa: E402, F401
from app.signup_credit.persistence import SignupCreditClaim  # noqa: E402, F401
from app.site_announcements.persistence import SiteAnnouncement  # noqa: E402, F401
from app.studio_models.model import WorkspaceStudioModel  # noqa: E402, F401


def _build_connect_args() -> dict:
    """Build driver connect_args, including a protective idle-in-transaction
    timeout for asyncpg connections.

    A single abandoned ``idle in transaction`` session can hold table/row locks
    indefinitely and wedge writes plus boot-time DDL (the classic "FastAPI
    stuck at application startup" failure). Setting
    ``idle_in_transaction_session_timeout`` server-side makes Postgres reap such
    sessions automatically. It never affects sessions that are actively running
    statements — only ones that opened a transaction and went idle.
    """
    connect_args: dict = {}
    idle_ms = config.DB_IDLE_IN_TX_TIMEOUT_MS
    # ``server_settings`` is asyncpg-specific; only apply it for that driver.
    if idle_ms and idle_ms > 0 and DATABASE_URL and "asyncpg" in DATABASE_URL:
        connect_args["server_settings"] = {
            "idle_in_transaction_session_timeout": str(idle_ms)
        }
    return connect_args


engine = create_async_engine(
    DATABASE_URL,
    pool_size=30,
    max_overflow=150,
    pool_recycle=1800,
    pool_pre_ping=True,
    pool_timeout=30,
    connect_args=_build_connect_args(),
)
async_session_maker = async_sessionmaker(engine, expire_on_commit=False)


@asynccontextmanager
async def shielded_async_session():
    """Cancellation-safe async session context manager.

    Starlette's BaseHTTPMiddleware cancels the task via an anyio cancel
    scope when a client disconnects.  A plain ``async with async_session_maker()``
    has its ``__aexit__`` (which awaits ``session.close()``) cancelled by the
    scope, orphaning the underlying database connection.

    This wrapper ensures ``session.close()`` always completes by running it
    inside ``anyio.CancelScope(shield=True)``.
    """
    session = async_session_maker()
    try:
        yield session
    finally:
        with anyio.CancelScope(shield=True):
            await session.close()


# (index_name, table, CREATE statement). Built with CONCURRENTLY so an index
# build only takes a non-blocking ShareUpdateExclusiveLock — ingestion
# INSERT/UPDATE on documents/chunks keep flowing while the index builds, and a
# slow build can never freeze the FastAPI lifespan or block writers.
_INDEX_DEFINITIONS: list[tuple[str, str, str]] = [
    (
        "document_vector_index",
        "documents",
        "CREATE INDEX CONCURRENTLY IF NOT EXISTS document_vector_index ON documents USING hnsw (embedding public.vector_cosine_ops)",
    ),
    (
        "document_search_index",
        "documents",
        "CREATE INDEX CONCURRENTLY IF NOT EXISTS document_search_index ON documents USING gin (to_tsvector('english', content))",
    ),
    (
        "chucks_vector_index",
        "chunks",
        "CREATE INDEX CONCURRENTLY IF NOT EXISTS chucks_vector_index ON chunks USING hnsw (embedding public.vector_cosine_ops)",
    ),
    (
        "chucks_search_index",
        "chunks",
        "CREATE INDEX CONCURRENTLY IF NOT EXISTS chucks_search_index ON chunks USING gin (to_tsvector('english', content))",
    ),
    # pg_trgm index for efficient ILIKE '%term%' searches on titles — critical
    # for the document mention picker (@mentions) to scale.
    (
        "idx_documents_title_trgm",
        "documents",
        "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_documents_title_trgm ON documents USING gin (title gin_trgm_ops)",
    ),
    (
        "idx_documents_workspace_id",
        "documents",
        "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_documents_workspace_id ON documents (workspace_id)",
    ),
    # Covering index for "recent documents" query — enables index-only scan.
    (
        "idx_documents_workspace_updated",
        "documents",
        "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_documents_workspace_updated ON documents (workspace_id, updated_at DESC NULLS LAST) INCLUDE (id, title, document_type)",
    ),
]


async def _drop_invalid_index(conn, name: str) -> None:
    """Drop a leftover *invalid* index so it can be rebuilt.

    A ``CREATE INDEX CONCURRENTLY`` that is interrupted (timeout, crash,
    cancellation) leaves behind an ``indisvalid = false`` index. Because the
    name now exists, a later ``CREATE INDEX CONCURRENTLY IF NOT EXISTS`` would
    skip it and the broken index would persist forever. Detect and drop it
    first.
    """
    result = await conn.execute(
        text("SELECT indisvalid FROM pg_index WHERE indexrelid = to_regclass(:n)"),
        {"n": name},
    )
    row = result.first()
    if row is not None and row[0] is False:
        logger.warning(
            "[startup] dropping invalid leftover index %s before rebuild", name
        )
        await conn.execute(text(f'DROP INDEX CONCURRENTLY IF EXISTS "{name}"'))


async def setup_indexes() -> None:
    """Ensure search/vector indexes exist without ever blocking startup.

    Each index is created with ``CONCURRENTLY`` (so it never takes a blocking
    SHARE lock on documents/chunks) under a short per-session ``lock_timeout``
    (so a contended boot fails fast instead of hanging the lifespan forever).
    Failures are logged and swallowed per-index — a missing index just gets
    retried on the next boot rather than crash-looping the API.
    """
    lock_timeout_ms = int(config.DB_DDL_LOCK_TIMEOUT_MS)
    # AUTOCOMMIT is mandatory: CREATE INDEX CONCURRENTLY cannot run inside a
    # transaction block.
    async with engine.connect() as base_conn:
        conn = await base_conn.execution_options(isolation_level="AUTOCOMMIT")
        await conn.execute(text(f"SET lock_timeout = {lock_timeout_ms}"))
        for name, table, ddl in _INDEX_DEFINITIONS:
            try:
                await _drop_invalid_index(conn, name)
                await conn.execute(text(ddl))
            except Exception as exc:
                # Non-fatal by design: a missing index is retried next boot.
                logger.warning(
                    "[startup] index %s on %s not ready (%s: %s); "
                    "will retry on next boot",
                    name,
                    table,
                    exc.__class__.__name__,
                    exc,
                )


async def create_db_and_tables():
    if not config.DB_BOOTSTRAP_ON_STARTUP:
        logger.info(
            "[startup] DB bootstrap skipped (DB_BOOTSTRAP_ON_STARTUP=FALSE); "
            "schema/indexes are expected to be managed by migrations"
        )
        return

    lock_timeout_ms = int(config.DB_DDL_LOCK_TIMEOUT_MS)
    async with engine.begin() as conn:
        # Fail fast instead of hanging forever if another session holds a
        # conflicting lock on a table we need to touch.
        await conn.execute(text(f"SET LOCAL lock_timeout = {lock_timeout_ms}"))
        await conn.execute(text("CREATE EXTENSION IF NOT EXISTS vector"))
        await conn.execute(text("CREATE EXTENSION IF NOT EXISTS pg_trgm"))
        await conn.run_sync(Base.metadata.create_all)
        # create_all never creates zero_publication (a migration-only
        # artifact), and without it zero-cache crash-loops. Idempotent.
        from app.zero_publication import ensure_publication

        await conn.run_sync(ensure_publication)
    await setup_indexes()


async def get_async_session() -> AsyncGenerator[AsyncSession, None]:
    async with async_session_maker() as session:
        yield session


if config.AUTH_TYPE == "GOOGLE":

    async def get_user_db(session: AsyncSession = Depends(get_async_session)):
        yield SQLAlchemyUserDatabase(session, User, OAuthAccount)

else:

    async def get_user_db(session: AsyncSession = Depends(get_async_session)):
        yield SQLAlchemyUserDatabase(session, User)


def has_permission(user_permissions: list[str], required_permission: str) -> bool:
    """
    Check if the user has the required permission.
    Supports wildcard (*) for full access.

    Args:
        user_permissions: List of permission strings the user has
        required_permission: The permission string to check for

    Returns:
        True if user has the permission, False otherwise
    """
    if not user_permissions:
        return False

    # Full access wildcard grants all permissions
    if Permission.FULL_ACCESS.value in user_permissions:
        return True

    return required_permission in user_permissions


def has_any_permission(
    user_permissions: list[str], required_permissions: list[str]
) -> bool:
    """
    Check if the user has any of the required permissions.

    Args:
        user_permissions: List of permission strings the user has
        required_permissions: List of permission strings to check for (any match)

    Returns:
        True if user has at least one of the permissions, False otherwise
    """
    if not user_permissions:
        return False

    if Permission.FULL_ACCESS.value in user_permissions:
        return True

    return any(perm in user_permissions for perm in required_permissions)


def has_all_permissions(
    user_permissions: list[str], required_permissions: list[str]
) -> bool:
    """
    Check if the user has all of the required permissions.

    Args:
        user_permissions: List of permission strings the user has
        required_permissions: List of permission strings to check for (all must match)

    Returns:
        True if user has all of the permissions, False otherwise
    """
    if not user_permissions:
        return False

    if Permission.FULL_ACCESS.value in user_permissions:
        return True

    return all(perm in user_permissions for perm in required_permissions)


def get_default_roles_config() -> list[dict]:
    """
    Get the configuration for default system roles.
    These roles are created automatically when a workspace is created.

    Only 3 roles are supported:
    - Owner: Full access to everything (assigned to workspace creator)
    - Editor: Can create/update content but cannot delete, manage roles, or change settings
    - Viewer: Read-only access to resources (can add comments)

    Returns:
        List of role configurations with name, description, permissions, and flags
    """
    return [
        {
            "name": "Owner",
            "description": "Full access to all workspace resources and settings",
            "permissions": DEFAULT_ROLE_PERMISSIONS["Owner"],
            "is_default": False,
            "is_system_role": True,
        },
        {
            "name": "Editor",
            "description": "Can create and update content (no delete, role management, or settings access)",
            "permissions": DEFAULT_ROLE_PERMISSIONS["Editor"],
            "is_default": True,  # Default role for new members via invite
            "is_system_role": True,
        },
        {
            "name": "Viewer",
            "description": "Read-only access to workspace resources",
            "permissions": DEFAULT_ROLE_PERMISSIONS["Viewer"],
            "is_default": False,
            "is_system_role": True,
        },
    ]
