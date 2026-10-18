"""
Routes for the new chat feature with assistant-ui integration.

These endpoints support the ThreadHistoryAdapter pattern from assistant-ui:
- GET /threads - List threads for sidebar (ThreadListPrimitive)
- POST /threads - Create a new thread
- GET /threads/{thread_id} - Get thread with messages (load)
- PUT /threads/{thread_id} - Update thread (rename, archive)
- DELETE /threads/{thread_id} - Delete thread
- POST /threads/{thread_id}/messages - Append message
"""

import asyncio
import json
import logging
from datetime import UTC, datetime

from fastapi import APIRouter, Depends, HTTPException, Request, Response
from fastapi.responses import StreamingResponse
from sqlalchemy import func, or_, text as sa_text
from sqlalchemy.dialects.postgresql import insert as pg_insert
from sqlalchemy.exc import IntegrityError, OperationalError
from sqlalchemy.ext.asyncio import AsyncSession
from sqlalchemy.future import select
from sqlalchemy.orm import selectinload

from app.agents.chat.multi_agent_chat.main_agent.middleware.busy_mutex import (
    get_cancel_state,
    is_cancel_requested,
    manager,
    request_cancel,
)
from app.agents.chat.multi_agent_chat.shared.filesystem_selection import (
    ClientPlatform,
    FilesystemMode,
    FilesystemSelection,
    LocalFilesystemMount,
)
from app.agents.chat.tool_preference import scope_for_tool_preference
from app.auth.context import AuthContext
from app.config import config
from app.db import (
    ARTIFACT_API_SOURCE,
    ChatComment,
    ChatVisibility,
    NewChatMessage,
    NewChatMessageRole,
    NewChatThread,
    Permission,
    TokenUsage,
    User,
    Workspace,
    get_async_session,
    shielded_async_session,
)
from app.observability.analytics import posthog as ph_analytics
from app.public_sharing.expiry import PublicLinkCreate
from app.push import events as push_events
from app.schemas.new_chat import (
    AgentToolInfo,
    CancelActiveTurnResponse,
    LocalFilesystemMountPayload,
    NewChatMessageRead,
    NewChatRequest,
    NewChatThreadCreate,
    NewChatThreadRead,
    NewChatThreadUpdate,
    NewChatThreadVisibilityUpdate,
    NewChatThreadWithMessages,
    PendingInterruptsResponse,
    PublicChatSnapshotCreateResponse,
    PublicChatSnapshotListResponse,
    RegenerateRequest,
    ResumeRequest,
    ThreadHistoryLoadResponse,
    ThreadListItem,
    ThreadListResponse,
    TokenUsageSummary,
    TurnStatusResponse,
)
from app.tasks.chat.streaming.detached_turn import (
    STOP_WAIT_SECONDS,
    is_turn_running,
    is_turn_stopping,
    run_cleanup_to_the_end,
    start_turn_in_background,
    stop_running_turn,
    wait_for_running_turn,
)
from app.tasks.chat.streaming.errors.messages import chat_error_message
from app.tasks.chat.streaming.flows import (
    stream_new_chat,
    stream_resume_chat,
)
from app.tasks.chat.streaming.flows.regenerate import (
    FromMessageNotInThreadError,
    find_replaced_turn,
    find_rewind_point,
    remove_replaced_messages,
)
from app.users import get_auth_context
from app.utils.perf import get_perf_logger
from app.utils.rbac import check_permission

_logger = logging.getLogger(__name__)
_perf_log = get_perf_logger()
_background_tasks: set[asyncio.Task] = set()
TURN_CANCELLING_INITIAL_DELAY_MS = 200
TURN_CANCELLING_BACKOFF_FACTOR = 2
TURN_CANCELLING_MAX_DELAY_MS = 1500

router = APIRouter()


def _resolve_filesystem_selection(
    *,
    mode: str,
    client_platform: str,
    local_mounts: list[LocalFilesystemMountPayload] | None,
) -> FilesystemSelection:
    """Validate and normalize filesystem mode settings from request payload."""
    try:
        resolved_mode = FilesystemMode(mode)
    except ValueError as exc:
        raise HTTPException(status_code=400, detail="Invalid filesystem_mode") from exc
    try:
        resolved_platform = ClientPlatform(client_platform)
    except ValueError as exc:
        raise HTTPException(status_code=400, detail="Invalid client_platform") from exc

    if resolved_mode == FilesystemMode.DESKTOP_LOCAL_FOLDER:
        if not config.ENABLE_DESKTOP_LOCAL_FILESYSTEM:
            raise HTTPException(
                status_code=400,
                detail="Desktop local filesystem mode is disabled on this deployment.",
            )
        if resolved_platform != ClientPlatform.DESKTOP:
            raise HTTPException(
                status_code=400,
                detail="desktop_local_folder mode is only available on desktop runtime.",
            )
        normalized_mounts: list[tuple[str, str]] = []
        seen_mounts: set[str] = set()
        for mount in local_mounts or []:
            mount_id = mount.mount_id.strip()
            root_path = mount.root_path.strip()
            if not mount_id or not root_path:
                continue
            if mount_id in seen_mounts:
                continue
            seen_mounts.add(mount_id)
            normalized_mounts.append((mount_id, root_path))
        if not normalized_mounts:
            raise HTTPException(
                status_code=400,
                detail=(
                    "local_filesystem_mounts must include at least one mount for "
                    "desktop_local_folder mode."
                ),
            )
        return FilesystemSelection(
            mode=resolved_mode,
            client_platform=resolved_platform,
            local_mounts=tuple(
                LocalFilesystemMount(mount_id=mount_id, root_path=root_path)
                for mount_id, root_path in normalized_mounts
            ),
        )

    return FilesystemSelection(
        mode=FilesystemMode.CLOUD,
        client_platform=resolved_platform,
    )


def _compute_turn_cancelling_retry_delay(attempt: int) -> int:
    """Bounded exponential delay for TURN_CANCELLING retry hints."""
    if attempt < 1:
        attempt = 1
    delay = TURN_CANCELLING_INITIAL_DELAY_MS * (
        TURN_CANCELLING_BACKOFF_FACTOR ** (attempt - 1)
    )
    return min(delay, TURN_CANCELLING_MAX_DELAY_MS)


def _build_turn_status_payload(thread_id: int) -> dict[str, object]:
    # A turn counts as running from the moment its background task starts
    # (before the agent takes the thread lock) until it has saved its answer
    # (after the lock is released), so no page sees "idle" in between.
    lock = manager.lock_for(str(thread_id))
    if not lock.locked() and not is_turn_running(str(thread_id)):
        return {"status": "idle"}

    # A stopped turn releases the lock and clears the cancel flag first, then
    # saves; it still reads "cancelling" until that save is done, so pages
    # show "Stopping…" rather than offering Stop again.
    if is_cancel_requested(str(thread_id)) or is_turn_stopping(str(thread_id)):
        cancel_state = get_cancel_state(str(thread_id))
        attempt = cancel_state[0] if cancel_state else 1
        retry_after_ms = _compute_turn_cancelling_retry_delay(attempt)
        retry_after_at = int(datetime.now(UTC).timestamp() * 1000) + retry_after_ms
        return {
            "status": "cancelling",
            "retry_after_ms": retry_after_ms,
            "retry_after_at": retry_after_at,
        }

    return {"status": "busy"}


def _set_retry_after_headers(response: Response, retry_after_ms: int) -> None:
    response.headers["retry-after-ms"] = str(retry_after_ms)
    response.headers["Retry-After"] = str(max(1, (retry_after_ms + 999) // 1000))


def _raise_if_thread_busy_for_start(thread_id: int) -> None:
    status_payload = _build_turn_status_payload(thread_id)
    status = status_payload["status"]
    if status == "idle":
        return
    if status == "cancelling":
        retry_after_ms = int(status_payload.get("retry_after_ms") or 0)
        detail = {
            "errorCode": "TURN_CANCELLING",
            "message": chat_error_message("TURN_CANCELLING"),
            "retry_after_ms": retry_after_ms if retry_after_ms > 0 else None,
            "retry_after_at": status_payload.get("retry_after_at"),
        }
        headers = (
            {
                "retry-after-ms": str(retry_after_ms),
                "Retry-After": str(max(1, (retry_after_ms + 999) // 1000)),
            }
            if retry_after_ms > 0
            else None
        )
        raise HTTPException(status_code=409, detail=detail, headers=headers)

    raise HTTPException(
        status_code=409,
        detail={
            "errorCode": "THREAD_BUSY",
            "message": chat_error_message("THREAD_BUSY"),
        },
    )


async def _raise_if_thread_awaiting_approval(thread_id: int, checkpointer) -> None:
    """Refuse a fresh turn when the thread's checkpoint has a pending interrupt.

    The busy mutex releases on an ``interrupt()`` pause, so a paused thread
    reads as idle to ``_raise_if_thread_busy_for_start``. Running ``new_chat`` /
    ``regenerate`` over that checkpoint would orphan the pending approval — the
    user must resume or cancel it first. ``resume`` is exempt; it's the path
    that clears the pause.
    """
    from app.tasks.chat.streaming.helpers.interrupt_inspector import (
        pending_interrupt_entries_from_writes,
    )

    checkpoint_tuple = await checkpointer.aget_tuple(
        {"configurable": {"thread_id": str(thread_id)}}
    )
    if checkpoint_tuple is None:
        return
    if pending_interrupt_entries_from_writes(checkpoint_tuple.pending_writes):
        raise HTTPException(
            status_code=409,
            detail={
                "errorCode": "THREAD_AWAITING_APPROVAL",
                "message": chat_error_message("THREAD_AWAITING_APPROVAL"),
            },
        )


async def _revert_turns_for_regenerate(
    *,
    thread_id: int,
    chat_turn_ids: list[str],
    requester_user_id: str,
) -> dict:
    """Best-effort revert pass for every ``chat_turn_id`` in ``chat_turn_ids``.

    Runs BEFORE the regenerate stream so the frontend can surface
    partial-rollback feedback alongside the new assistant turn. Each
    turn's actions are reverted in their own SAVEPOINTs (handled
    inside :mod:`app.routes.agent_revert_route`'s helpers) so a single
    failure never poisons the batch.

    Sequencing inside the request: revert THEN regenerate. The
    operation is NOT atomic and partial state IS surfaced — see the
    plan's "Sequencing inside the request" note.
    """

    from app.routes.agent_revert_route import (
        RevertTurnActionResult,
        _classify_outcome,
        _OutcomeRollbackError,
        _was_already_reverted,
        _was_already_reverted_batch,
    )
    from app.services.revert_service import (
        can_revert,
        revert_action,
    )

    aggregated_results: list[dict] = []
    # Exhaustive counters keep the response invariant
    # ``total == sum(counters)`` true for ``data-revert-results``.
    counts = {
        "reverted": 0,
        "already_reverted": 0,
        "not_reversible": 0,
        "permission_denied": 0,
        "failed": 0,
        "skipped": 0,
    }

    # Local import keeps the route module's existing imports tidy and
    # avoids a circular dependency at module-load time.
    from app.db import AgentActionLog as _AgentActionLog

    async with shielded_async_session() as session:
        for chat_turn_id in chat_turn_ids:
            rows_stmt = (
                select(_AgentActionLog)
                .where(
                    _AgentActionLog.thread_id == thread_id,
                    _AgentActionLog.chat_turn_id == chat_turn_id,
                )
                .order_by(
                    _AgentActionLog.created_at.desc(),
                    _AgentActionLog.id.desc(),
                )
            )
            rows = (await session.execute(rows_stmt)).scalars().all()

            # Batch idempotency probe across the turn (single SELECT
            # instead of one per row).
            eligible_ids = [r.id for r in rows if r.reverse_of is None]
            already_reverted_map = await _was_already_reverted_batch(
                session, action_ids=eligible_ids
            )

            for action in rows:
                if action.reverse_of is not None:
                    counts["skipped"] += 1
                    aggregated_results.append(
                        RevertTurnActionResult(
                            action_id=action.id,
                            tool_name=action.tool_name,
                            status="skipped",
                            message="Row is itself a revert action; skipped.",
                        ).model_dump()
                    )
                    continue

                existing_revert_id = already_reverted_map.get(action.id)
                if existing_revert_id is not None:
                    counts["already_reverted"] += 1
                    aggregated_results.append(
                        RevertTurnActionResult(
                            action_id=action.id,
                            tool_name=action.tool_name,
                            status="already_reverted",
                            new_action_id=existing_revert_id,
                        ).model_dump()
                    )
                    continue

                if not can_revert(
                    requester_user_id=requester_user_id,
                    action=action,
                    is_admin=False,
                ):
                    counts["permission_denied"] += 1
                    aggregated_results.append(
                        RevertTurnActionResult(
                            action_id=action.id,
                            tool_name=action.tool_name,
                            status="permission_denied",
                            message="You are not allowed to revert this action.",
                        ).model_dump()
                    )
                    continue

                try:
                    async with session.begin_nested():
                        outcome = await revert_action(
                            session,
                            action=action,
                            requester_user_id=requester_user_id,
                        )
                        if outcome.status != "ok":
                            raise _OutcomeRollbackError(outcome)
                except _OutcomeRollbackError as rollback:
                    outcome = rollback.outcome
                    classified = _classify_outcome(outcome)
                    if classified == "permission_denied":
                        counts["permission_denied"] += 1
                    else:
                        counts["not_reversible"] += 1
                    aggregated_results.append(
                        RevertTurnActionResult(
                            action_id=action.id,
                            tool_name=action.tool_name,
                            status=classified,
                            message=outcome.message,
                        ).model_dump()
                    )
                    continue
                except IntegrityError:
                    # Concurrent revert won the race against the
                    # pre-flight ``_was_already_reverted`` SELECT.
                    # Surface the winning revert id so the client can
                    # treat this as a successful idempotent op.
                    existing_revert_id = await _was_already_reverted(
                        session, action_id=action.id
                    )
                    counts["already_reverted"] += 1
                    aggregated_results.append(
                        RevertTurnActionResult(
                            action_id=action.id,
                            tool_name=action.tool_name,
                            status="already_reverted",
                            new_action_id=existing_revert_id,
                        ).model_dump()
                    )
                    continue
                except Exception as err:  # pragma: no cover — defensive
                    _logger.exception(
                        "Unexpected revert failure during regenerate batch "
                        "for action_id=%s",
                        action.id,
                    )
                    counts["failed"] += 1
                    aggregated_results.append(
                        RevertTurnActionResult(
                            action_id=action.id,
                            tool_name=action.tool_name,
                            status="failed",
                            error=str(err) or err.__class__.__name__,
                        ).model_dump()
                    )
                    continue

                counts["reverted"] += 1
                aggregated_results.append(
                    RevertTurnActionResult(
                        action_id=action.id,
                        tool_name=action.tool_name,
                        status="reverted",
                        message=outcome.message,
                        new_action_id=outcome.new_action_id,
                    ).model_dump()
                )

        try:
            await session.commit()
        except Exception:
            _logger.exception(
                "[regenerate-revert] Final commit failed; rolling back batch."
            )
            await session.rollback()

    has_partial = (
        counts["failed"] > 0
        or counts["not_reversible"] > 0
        or counts["permission_denied"] > 0
    )

    return {
        "status": "partial" if has_partial else "ok",
        "chat_turn_ids": chat_turn_ids,
        "total": len(aggregated_results),
        "reverted": counts["reverted"],
        "already_reverted": counts["already_reverted"],
        "not_reversible": counts["not_reversible"],
        "permission_denied": counts["permission_denied"],
        "failed": counts["failed"],
        "skipped": counts["skipped"],
        "results": aggregated_results,
    }


def _try_delete_sandbox(thread_id: int) -> None:
    """Fire-and-forget sandbox + local file deletion so the HTTP response isn't blocked."""
    from app.agents.chat.multi_agent_chat.shared.middleware.filesystem.sandbox import (
        delete_local_sandbox_files,
        delete_sandbox,
    )
    from app.sandbox import is_sandbox_enabled

    if not is_sandbox_enabled():
        return

    async def _bg() -> None:
        try:
            await delete_sandbox(thread_id)
        except Exception:
            _logger.warning(
                "Background sandbox delete failed for thread %s",
                thread_id,
                exc_info=True,
            )
        try:
            delete_local_sandbox_files(thread_id)
        except Exception:
            _logger.warning(
                "Local sandbox file cleanup failed for thread %s",
                thread_id,
                exc_info=True,
            )

    try:
        loop = asyncio.get_running_loop()
        task = loop.create_task(_bg())
        _background_tasks.add(task)
        task.add_done_callback(_background_tasks.discard)
    except RuntimeError:
        pass


async def check_thread_access(
    session: AsyncSession,
    thread: NewChatThread,
    user: User,
    require_ownership: bool = False,
) -> bool:
    """
    Check if a user has access to a thread based on visibility rules.

    Access is granted if:
    - User is the creator of the thread
    - Thread visibility is SEARCH_SPACE (any member can access) - for read/update operations only
    - Thread is a legacy thread (created_by_id is NULL) - only if user is workspace owner

    Args:
        session: Database session
        thread: The thread to check access for
        user: The user requesting access
        require_ownership: If True, ONLY the creator can perform this action (e.g., changing visibility).
                          This is checked FIRST, before visibility rules.

    Returns:
        True if access is granted

    Raises:
        HTTPException: If access is denied
    """
    is_owner = thread.created_by_id == user.id
    is_legacy = thread.created_by_id is None

    # If ownership is required (e.g., changing visibility), ONLY the creator can do it
    # This check comes first to ensure ownership-required operations are always creator-only
    if require_ownership:
        if not is_owner:
            raise HTTPException(
                status_code=403,
                detail="Only the creator of this chat can perform this action",
            )
        return True

    # Shared threads (SEARCH_SPACE) are accessible by any member for read/update operations
    if thread.visibility == ChatVisibility.SEARCH_SPACE:
        return True

    # For legacy threads (created before visibility feature),
    # only the workspace owner can access
    if is_legacy:
        workspace_query = select(Workspace).filter(Workspace.id == thread.workspace_id)
        workspace_result = await session.execute(workspace_query)
        workspace = workspace_result.scalar_one_or_none()
        is_workspace_owner = workspace and workspace.user_id == user.id

        if is_workspace_owner:
            return True
        # Legacy threads are not accessible to non-owners
        raise HTTPException(
            status_code=403,
            detail="You don't have access to this chat",
        )

    # For read access: owner can access their own private threads
    if is_owner:
        return True

    # Private thread and user is not the owner
    raise HTTPException(
        status_code=403,
        detail="You don't have access to this private chat",
    )


# =============================================================================
# Thread Endpoints
# =============================================================================


@router.get("/threads", response_model=ThreadListResponse)
async def list_threads(
    workspace_id: int,
    limit: int | None = None,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    List all accessible threads for the current user in a workspace.
    Returns threads and archived_threads for ThreadListPrimitive.

    A user can see threads that are:
    - Created by them (regardless of visibility)
    - Shared with the workspace (visibility = SEARCH_SPACE)
    - Legacy threads with no creator (created_by_id is NULL) - only if user is workspace owner

    Args:
        workspace_id: The workspace to list threads for
        limit: Optional limit on number of threads to return (applies to active threads only)

    Requires CHATS_READ permission.
    """
    try:
        await check_permission(
            session,
            auth,
            workspace_id,
            Permission.CHATS_READ.value,
            "You don't have permission to read chats in this workspace",
        )

        # Check if user is the workspace owner (for legacy thread visibility)
        workspace_query = select(Workspace).filter(Workspace.id == workspace_id)
        workspace_result = await session.execute(workspace_query)
        workspace = workspace_result.scalar_one_or_none()
        is_workspace_owner = workspace and workspace.user_id == user.id

        # Build filter conditions:
        # 1. Created by the current user (any visibility)
        # 2. Shared with the workspace (visibility = SEARCH_SPACE)
        # 3. Legacy threads (created_by_id is NULL) - only visible to workspace owner
        filter_conditions = [
            NewChatThread.created_by_id == user.id,
            NewChatThread.visibility == ChatVisibility.SEARCH_SPACE,
        ]

        # Only include legacy threads for the workspace owner
        if is_workspace_owner:
            filter_conditions.append(NewChatThread.created_by_id.is_(None))

        query = (
            select(NewChatThread)
            .filter(
                NewChatThread.workspace_id == workspace_id,
                NewChatThread.source != ARTIFACT_API_SOURCE,
                or_(*filter_conditions),
            )
            .order_by(NewChatThread.updated_at.desc())
        )

        result = await session.execute(query)
        all_threads = result.scalars().all()

        # Separate active and archived threads
        threads = []
        archived_threads = []

        for thread in all_threads:
            # Legacy threads (no creator) are treated as own threads for owner
            is_own_thread = thread.created_by_id == user.id or (
                thread.created_by_id is None and is_workspace_owner
            )
            item = ThreadListItem(
                id=thread.id,
                title=thread.title,
                archived=thread.archived,
                visibility=thread.visibility,
                created_by_id=thread.created_by_id,
                is_own_thread=is_own_thread,
                created_at=thread.created_at,
                updated_at=thread.updated_at,
            )
            if thread.archived:
                archived_threads.append(item)
            else:
                threads.append(item)

        # Apply limit to active threads if specified
        if limit is not None and limit > 0:
            threads = threads[:limit]

        return ThreadListResponse(threads=threads, archived_threads=archived_threads)

    except HTTPException:
        raise
    except OperationalError:
        raise HTTPException(
            status_code=503, detail="Database operation failed. Please try again later."
        ) from None
    except Exception as e:
        raise HTTPException(
            status_code=500,
            detail=f"An unexpected error occurred while fetching threads: {e!s}",
        ) from None


@router.get("/threads/search", response_model=list[ThreadListItem])
async def search_threads(
    workspace_id: int,
    title: str,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    Search accessible threads by title in a workspace.

    A user can search threads that are:
    - Created by them (regardless of visibility)
    - Shared with the workspace (visibility = SEARCH_SPACE)
    - Legacy threads with no creator (created_by_id is NULL) - only if user is workspace owner

    Args:
        workspace_id: The workspace to search in
        title: The search query (case-insensitive partial match)

    Requires CHATS_READ permission.
    """
    try:
        await check_permission(
            session,
            auth,
            workspace_id,
            Permission.CHATS_READ.value,
            "You don't have permission to read chats in this workspace",
        )

        # Check if user is the workspace owner (for legacy thread visibility)
        workspace_query = select(Workspace).filter(Workspace.id == workspace_id)
        workspace_result = await session.execute(workspace_query)
        workspace = workspace_result.scalar_one_or_none()
        is_workspace_owner = workspace and workspace.user_id == user.id

        # Build filter conditions
        filter_conditions = [
            NewChatThread.created_by_id == user.id,
            NewChatThread.visibility == ChatVisibility.SEARCH_SPACE,
        ]

        # Only include legacy threads for the workspace owner
        if is_workspace_owner:
            filter_conditions.append(NewChatThread.created_by_id.is_(None))

        # Search accessible threads by title (case-insensitive)
        query = (
            select(NewChatThread)
            .filter(
                NewChatThread.workspace_id == workspace_id,
                NewChatThread.title.ilike(f"%{title}%"),
                NewChatThread.source != ARTIFACT_API_SOURCE,
                or_(*filter_conditions),
            )
            .order_by(NewChatThread.updated_at.desc())
        )

        result = await session.execute(query)
        threads = result.scalars().all()

        return [
            ThreadListItem(
                id=thread.id,
                title=thread.title,
                archived=thread.archived,
                visibility=thread.visibility,
                created_by_id=thread.created_by_id,
                # Legacy threads (no creator) are treated as own threads for owner
                is_own_thread=(
                    thread.created_by_id == user.id
                    or (thread.created_by_id is None and is_workspace_owner)
                ),
                created_at=thread.created_at,
                updated_at=thread.updated_at,
            )
            for thread in threads
        ]

    except HTTPException:
        raise
    except OperationalError:
        raise HTTPException(
            status_code=503, detail="Database operation failed. Please try again later."
        ) from None
    except Exception as e:
        raise HTTPException(
            status_code=500,
            detail=f"An unexpected error occurred while searching threads: {e!s}",
        ) from None


@router.post("/threads", response_model=NewChatThreadRead)
async def create_thread(
    thread: NewChatThreadCreate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    Create a new chat thread.

    The thread is created with the specified visibility (defaults to PRIVATE).
    The current user is recorded as the creator of the thread.

    Requires CHATS_CREATE permission.
    """
    try:
        await check_permission(
            session,
            auth,
            thread.workspace_id,
            Permission.CHATS_CREATE.value,
            "You don't have permission to create chats in this workspace",
        )

        now = datetime.now(UTC)
        db_thread = NewChatThread(
            title=thread.title,
            archived=thread.archived,
            visibility=thread.visibility,
            workspace_id=thread.workspace_id,
            created_by_id=user.id,
            updated_at=now,
        )
        session.add(db_thread)
        await session.commit()
        await session.refresh(db_thread)

        # Authoritative thread creation (migrated from stream-engine/engine.ts).
        # get_auth_context => covers PAT/MCP callers the frontend never sees.
        ph_analytics.capture_for(
            auth,
            "chat_created",
            {"workspace_id": db_thread.workspace_id, "chat_id": db_thread.id},
            groups={"workspace": str(db_thread.workspace_id)},
        )
        return db_thread

    except HTTPException:
        raise
    except IntegrityError:
        await session.rollback()
        raise HTTPException(
            status_code=400,
            detail="Database constraint violation. Please check your input data.",
        ) from None
    except OperationalError:
        await session.rollback()
        raise HTTPException(
            status_code=503, detail="Database operation failed. Please try again later."
        ) from None
    except Exception as e:
        await session.rollback()
        raise HTTPException(
            status_code=500,
            detail=f"An unexpected error occurred while creating the thread: {e!s}",
        ) from None


@router.get("/threads/{thread_id}", response_model=ThreadHistoryLoadResponse)
async def get_thread_messages(
    thread_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    Get a thread with all its messages.
    This is used by ThreadHistoryAdapter.load() to restore conversation.

    Access is granted if:
    - User is the creator of the thread
    - Thread visibility is SEARCH_SPACE

    Requires CHATS_READ permission.
    """
    try:
        # Get thread first
        result = await session.execute(
            select(NewChatThread).filter(NewChatThread.id == thread_id)
        )
        thread = result.scalars().first()

        if not thread:
            raise HTTPException(status_code=404, detail="Thread not found")

        # Check permission to read chats in this workspace
        await check_permission(
            session,
            auth,
            thread.workspace_id,
            Permission.CHATS_READ.value,
            "You don't have permission to read chats in this workspace",
        )

        # Check thread-level access based on visibility
        await check_thread_access(session, thread, user)

        # Get messages with their authors and token usage loaded
        messages_result = await session.execute(
            select(NewChatMessage)
            .options(
                selectinload(NewChatMessage.author),
                selectinload(NewChatMessage.token_usage),
            )
            .filter(NewChatMessage.thread_id == thread_id)
            .order_by(NewChatMessage.created_at)
        )
        db_messages = messages_result.scalars().all()

        # Return messages in the format expected by assistant-ui
        messages = [
            NewChatMessageRead(
                id=msg.id,
                thread_id=msg.thread_id,
                role=msg.role,
                content=msg.content,
                created_at=msg.created_at,
                author_id=msg.author_id,
                author_display_name=msg.author.display_name if msg.author else None,
                author_avatar_url=msg.author.avatar_url if msg.author else None,
                token_usage=TokenUsageSummary.model_validate(msg.token_usage)
                if msg.token_usage
                else None,
                turn_id=msg.turn_id,
            )
            for msg in db_messages
        ]

        return ThreadHistoryLoadResponse(messages=messages)

    except HTTPException:
        raise
    except OperationalError:
        raise HTTPException(
            status_code=503, detail="Database operation failed. Please try again later."
        ) from None
    except Exception as e:
        raise HTTPException(
            status_code=500,
            detail=f"An unexpected error occurred while fetching the thread: {e!s}",
        ) from None


@router.get("/threads/{thread_id}/full", response_model=NewChatThreadWithMessages)
async def get_thread_full(
    thread_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    Get full thread details with all messages.

    Access is granted if:
    - User is the creator of the thread
    - Thread visibility is SEARCH_SPACE

    Requires CHATS_READ permission.
    """
    try:
        result = await session.execute(
            select(NewChatThread)
            .options(
                selectinload(NewChatThread.messages).selectinload(
                    NewChatMessage.token_usage
                ),
            )
            .filter(NewChatThread.id == thread_id)
        )
        thread = result.scalars().first()

        if not thread:
            raise HTTPException(status_code=404, detail="Thread not found")

        await check_permission(
            session,
            auth,
            thread.workspace_id,
            Permission.CHATS_READ.value,
            "You don't have permission to read chats in this workspace",
        )

        # Check thread-level access based on visibility
        await check_thread_access(session, thread, user)

        # Check if thread has any comments
        comment_count = await session.scalar(
            select(func.count())
            .select_from(ChatComment)
            .join(NewChatMessage, ChatComment.message_id == NewChatMessage.id)
            .where(NewChatMessage.thread_id == thread.id)
        )

        return {
            **thread.__dict__,
            "messages": thread.messages,
            "has_comments": (comment_count or 0) > 0,
        }

    except HTTPException:
        raise
    except OperationalError:
        raise HTTPException(
            status_code=503, detail="Database operation failed. Please try again later."
        ) from None
    except Exception as e:
        raise HTTPException(
            status_code=500,
            detail=f"An unexpected error occurred while fetching the thread: {e!s}",
        ) from None


@router.put("/threads/{thread_id}", response_model=NewChatThreadRead)
async def update_thread(
    thread_id: int,
    thread_update: NewChatThreadUpdate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    Update a thread (title, archived status).
    Used for renaming and archiving threads.

    - PRIVATE threads: Only the creator can update
    - SEARCH_SPACE threads: Any member with CHATS_UPDATE permission can update

    Requires CHATS_UPDATE permission.
    """
    try:
        result = await session.execute(
            select(NewChatThread).filter(NewChatThread.id == thread_id)
        )
        db_thread = result.scalars().first()

        if not db_thread:
            raise HTTPException(status_code=404, detail="Thread not found")

        await check_permission(
            session,
            auth,
            db_thread.workspace_id,
            Permission.CHATS_UPDATE.value,
            "You don't have permission to update chats in this workspace",
        )

        # For PRIVATE threads, only the creator can update
        # For SEARCH_SPACE threads, any member with permission can update
        if db_thread.visibility == ChatVisibility.PRIVATE:
            await check_thread_access(session, db_thread, user, require_ownership=True)

        # Update fields
        update_data = thread_update.model_dump(exclude_unset=True)
        for key, value in update_data.items():
            setattr(db_thread, key, value)

        db_thread.updated_at = datetime.now(UTC)

        await session.commit()
        await session.refresh(db_thread)
        return db_thread

    except HTTPException:
        raise
    except IntegrityError:
        await session.rollback()
        raise HTTPException(
            status_code=400,
            detail="Database constraint violation. Please check your input data.",
        ) from None
    except OperationalError:
        await session.rollback()
        raise HTTPException(
            status_code=503, detail="Database operation failed. Please try again later."
        ) from None
    except Exception as e:
        await session.rollback()
        raise HTTPException(
            status_code=500,
            detail=f"An unexpected error occurred while updating the thread: {e!s}",
        ) from None


@router.delete("/threads/{thread_id}", response_model=dict)
async def delete_thread(
    thread_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    Delete a thread and all its messages.

    - PRIVATE threads: Only the creator can delete
    - SEARCH_SPACE threads: Any member with CHATS_DELETE permission can delete

    Requires CHATS_DELETE permission.
    """
    try:
        result = await session.execute(
            select(NewChatThread).filter(NewChatThread.id == thread_id)
        )
        db_thread = result.scalars().first()

        if not db_thread:
            raise HTTPException(status_code=404, detail="Thread not found")

        await check_permission(
            session,
            auth,
            db_thread.workspace_id,
            Permission.CHATS_DELETE.value,
            "You don't have permission to delete chats in this workspace",
        )

        # For PRIVATE threads, only the creator can delete
        # For SEARCH_SPACE threads, any member with permission can delete
        # Legacy threads (created_by_id is NULL) have no recorded creator,
        # so we skip strict ownership and fall through to legacy handling
        # which allows the workspace owner to delete them
        if db_thread.visibility == ChatVisibility.PRIVATE:
            await check_thread_access(
                session,
                db_thread,
                user,
                require_ownership=(db_thread.created_by_id is not None),
            )

        await session.delete(db_thread)
        await session.commit()

        _try_delete_sandbox(thread_id)

        return {"message": "Thread deleted successfully"}

    except HTTPException:
        raise
    except IntegrityError:
        await session.rollback()
        raise HTTPException(
            status_code=400, detail="Cannot delete thread due to existing dependencies."
        ) from None
    except OperationalError:
        await session.rollback()
        raise HTTPException(
            status_code=503, detail="Database operation failed. Please try again later."
        ) from None
    except Exception as e:
        await session.rollback()
        raise HTTPException(
            status_code=500,
            detail=f"An unexpected error occurred while deleting the thread: {e!s}",
        ) from None


@router.patch("/threads/{thread_id}/visibility", response_model=NewChatThreadRead)
async def update_thread_visibility(
    thread_id: int,
    visibility_update: NewChatThreadVisibilityUpdate,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    Update the visibility/sharing settings of a thread.

    Only the creator of the thread can change its visibility.
    - PRIVATE: Only the creator can access the thread (default)
    - SEARCH_SPACE: All members of the workspace can access the thread

    Requires CHATS_UPDATE permission.
    """
    try:
        result = await session.execute(
            select(NewChatThread).filter(NewChatThread.id == thread_id)
        )
        db_thread = result.scalars().first()

        if not db_thread:
            raise HTTPException(status_code=404, detail="Thread not found")

        await check_permission(
            session,
            auth,
            db_thread.workspace_id,
            Permission.CHATS_UPDATE.value,
            "You don't have permission to update chats in this workspace",
        )

        # Only the creator can change visibility
        await check_thread_access(session, db_thread, user, require_ownership=True)

        # Update visibility
        db_thread.visibility = visibility_update.visibility
        db_thread.updated_at = datetime.now(UTC)

        await session.commit()
        await session.refresh(db_thread)
        return db_thread

    except HTTPException:
        raise
    except IntegrityError:
        await session.rollback()
        raise HTTPException(
            status_code=400,
            detail="Database constraint violation. Please check your input data.",
        ) from None
    except OperationalError:
        await session.rollback()
        raise HTTPException(
            status_code=503, detail="Database operation failed. Please try again later."
        ) from None
    except Exception as e:
        await session.rollback()
        raise HTTPException(
            status_code=500,
            detail=f"An unexpected error occurred while updating thread visibility: {e!s}",
        ) from None


# =============================================================================
# Snapshot Endpoints
# =============================================================================


@router.post(
    "/threads/{thread_id}/snapshots", response_model=PublicChatSnapshotCreateResponse
)
async def create_thread_snapshot(
    thread_id: int,
    body: PublicLinkCreate | None = None,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Create a read-only public link to the thread (Owners, by default).

    Expires after ``expires_in_days`` (7 unless given; null never expires).
    Returns the existing link if the content is unchanged (deduplication).
    """
    from app.services.public_chat_service import create_snapshot

    return await create_snapshot(
        session=session,
        thread_id=thread_id,
        auth=auth,
        expires_in_days=(body or PublicLinkCreate()).expires_in_days,
    )


@router.get(
    "/threads/{thread_id}/snapshots", response_model=PublicChatSnapshotListResponse
)
async def list_thread_snapshots(
    thread_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    List all public snapshots for this thread.

    Only the thread owner can view snapshots.
    """
    from app.services.public_chat_service import list_snapshots_for_thread

    return PublicChatSnapshotListResponse(
        snapshots=await list_snapshots_for_thread(
            session=session,
            thread_id=thread_id,
            auth=auth,
        )
    )


@router.delete("/threads/{thread_id}/snapshots/{snapshot_id}")
async def delete_thread_snapshot(
    thread_id: int,
    snapshot_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """
    Delete a specific snapshot.

    Only the thread owner can delete snapshots.
    """
    from app.services.public_chat_service import delete_snapshot

    await delete_snapshot(
        session=session,
        thread_id=thread_id,
        snapshot_id=snapshot_id,
        auth=auth,
    )
    return {"message": "Snapshot deleted successfully"}


# =============================================================================
# Message Endpoints
# =============================================================================


@router.post("/threads/{thread_id}/messages", response_model=NewChatMessageRead)
async def append_message(
    thread_id: int,
    request: Request,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    .. deprecated:: 2026-05
       Replaced by the **SSE-based message ID handshake**. The streaming
       generator (`stream_new_chat` / `stream_resume_chat`) now persists
       both the user and assistant rows server-side via
       ``persist_user_turn`` / ``persist_assistant_shell`` and emits
       ``data-user-message-id`` / ``data-assistant-message-id`` SSE events
       so the frontend can rename its optimistic IDs in real time. The
       new FE bundle no longer calls this route.

       This handler is retained as a **silent no-op for legacy / cached
       FE bundles**: the underlying ``INSERT ... ON CONFLICT DO NOTHING``
       pattern means a stale bundle hitting this route after the SSE
       handshake already wrote the row simply returns the existing row
       (200 OK) without raising or duplicating data. After a 2-week soak
       (target: ``[persist_user_turn] outcome=race_recovered`` rate ~0)
       this entire route — and the FE ``appendMessage`` function — is
       earmarked for removal.

    Append a message to a thread.
    This is used by ThreadHistoryAdapter.append() to persist messages.

    Access is granted if:
    - User is the creator of the thread
    - Thread visibility is SEARCH_SPACE

    Requires CHATS_UPDATE permission.
    """
    try:
        # Capture ``user.id`` as a primitive UUID up front. The auth
        # dependency hands us a ``User`` ORM
        # row bound to ``session``; if the outer ``except
        # IntegrityError`` block below ever fires (an unexpected
        # constraint like a foreign key violation — the common
        # ``(thread_id, turn_id, role)`` race is now handled silently
        # by ``ON CONFLICT DO NOTHING`` so it never raises) it calls
        # ``session.rollback()``, which expires every attached ORM
        # row including this user. Any later ``user.id`` access would
        # then trigger a lazy PK reload — which on async SQLAlchemy
        # fails with ``MissingGreenlet`` because the reload happens
        # outside the awaitable greenlet boundary. Reading ``id``
        # once here pins the value as a plain UUID so all downstream
        # uses (TokenUsage insert, response build) are immune.
        user_uuid = user.id

        # Parse raw body - extract only role and content, ignoring extra fields
        raw_body = await request.json()
        role = raw_body.get("role")
        content = raw_body.get("content")

        if not role:
            raise HTTPException(status_code=400, detail="Missing required field: role")
        if content is None:
            raise HTTPException(
                status_code=400, detail="Missing required field: content"
            )

        # Validate role early (before any DB work)
        role_str = role.lower() if isinstance(role, str) else role
        try:
            message_role = NewChatMessageRole(role_str)
        except ValueError:
            raise HTTPException(
                status_code=400,
                detail=f"Invalid role: {role}. Must be 'user', 'assistant', or 'system'.",
            ) from None

        # Get thread
        result = await session.execute(
            select(NewChatThread).filter(NewChatThread.id == thread_id)
        )
        thread = result.scalars().first()

        if not thread:
            raise HTTPException(status_code=404, detail="Thread not found")

        await check_permission(
            session,
            auth,
            thread.workspace_id,
            Permission.CHATS_UPDATE.value,
            "You don't have permission to update chats in this workspace",
        )

        # Check thread-level access based on visibility
        await check_thread_access(session, thread, user)

        # Create message. ``turn_id`` is the per-turn correlation id from
        # ``configurable.turn_id`` (added in migration 136) — when the
        # client streams it back to ``appendMessage``, we persist it so
        # C1's edit-from-arbitrary-position can later map this message
        # back to the LangGraph checkpoint that produced its turn.
        raw_turn_id = raw_body.get("turn_id")
        turn_id_value = (
            str(raw_turn_id).strip()
            if isinstance(raw_turn_id, str) and raw_turn_id.strip()
            else None
        )

        # Update thread's updated_at timestamp (always — both insert
        # and recovery paths represent thread activity).
        thread.updated_at = datetime.now(UTC)

        # Insert the new message via ``INSERT ... ON CONFLICT DO NOTHING``
        # keyed on the ``(thread_id, turn_id, role)`` partial unique
        # index from migration 141 (``WHERE turn_id IS NOT NULL``).
        #
        # Why ON CONFLICT instead of ``session.add() + flush() + except
        # IntegrityError``:
        #   1. The conflict between this legacy FE ``appendMessage``
        #      round-trip and the server-side
        #      ``finalize_assistant_turn`` writer is a NORMAL,
        #      *expected* race — every assistant turn fires it. Using
        #      catch-and-recover means asyncpg raises
        #      ``UniqueViolationError`` -> SQLAlchemy wraps it as
        #      ``IntegrityError`` -> our handler catches and recovers.
        #      Functionally fine, but every ``raise`` event lights up
        #      VS Code's debugger (debugpy's ``justMyCode=false`` mode
        #      loses track of the catch frame across SQLAlchemy's
        #      async greenlet boundary, so even ``Raised Exceptions``
        #      being unchecked doesn't reliably suppress the pause).
        #      ON CONFLICT pushes the conflict resolution into Postgres
        #      where no Python exception is constructed at all.
        #   2. No ``session.rollback()`` -> no expiring of attached
        #      ORM rows -> no risk of ``MissingGreenlet`` from
        #      lazy-loading expired user/thread state later in the
        #      handler.
        #   3. Cleaner production logs (no SQLAlchemy ``IntegrityError``
        #      tracebacks emitted by uvicorn's logger between the
        #      ``raise`` and our ``except``).
        #
        # When ``turn_id_value`` is ``None`` the partial index doesn't
        # apply and the INSERT proceeds normally. Other constraint
        # violations (FK, NOT NULL, etc.) still raise ``IntegrityError``
        # and are caught by the outer ``except IntegrityError`` block
        # to preserve the legacy 400 behavior.
        #
        # Note on ``content``: when we recover the existing row, we
        # intentionally discard the FE's ``content`` payload from
        # ``raw_body`` and return the row's existing ``content``. The
        # streaming task is now the *authoritative writer* for
        # assistant ``ContentPart[]`` shape (mid-stream
        # ``AssistantContentBuilder`` -> ``finalize_assistant_turn``)
        # so the FE's later ``appendMessage`` is just a stale snapshot
        # of the same data — keeping the server-built rich content
        # (with full tool-call args / argsText / langchainToolCallId)
        # is correct, not lossy.
        insert_stmt = (
            pg_insert(NewChatMessage)
            .values(
                thread_id=thread_id,
                role=message_role,
                content=content,
                author_id=user_uuid,
                turn_id=turn_id_value,
            )
            .on_conflict_do_nothing(
                index_elements=["thread_id", "turn_id", "role"],
                index_where=sa_text("turn_id IS NOT NULL"),
            )
            .returning(NewChatMessage.id)
        )
        inserted_id = (await session.execute(insert_stmt)).scalar()

        if inserted_id is None:
            # Conflict on partial unique index — server-side stream
            # already wrote this row. Look it up and reuse it.
            if turn_id_value is None:
                # Defensive: ON CONFLICT only fires for ``turn_id IS
                # NOT NULL`` rows, so this branch should be
                # unreachable. Preserve the legacy 400 just in case
                # Postgres ever surprises us.
                raise HTTPException(
                    status_code=400,
                    detail="Database constraint violation. Please check your input data.",
                ) from None
            lookup = await session.execute(
                select(NewChatMessage).filter(
                    NewChatMessage.thread_id == thread_id,
                    NewChatMessage.turn_id == turn_id_value,
                    NewChatMessage.role == message_role,
                )
            )
            existing_message = lookup.scalars().first()
            if existing_message is None:
                # Conflict reported but the row vanished between
                # INSERT and SELECT — extremely unlikely (would
                # require a concurrent DELETE within the same
                # transaction visibility), but preserve safe
                # behavior.
                raise HTTPException(
                    status_code=400,
                    detail="Database constraint violation. Please check your input data.",
                ) from None
            db_message = existing_message
            # Perf signal: counts how often the legacy FE round-trip
            # races the server-side ``finalize_assistant_turn``. A
            # rising rate after the rework is OK (it's exactly the
            # ghost-thread fix's recovery path firing); a sudden drop
            # to zero would mean the FE isn't posting appendMessage
            # at all (different bug).
            _perf_log.info(
                "[append_message] outcome=recovered_via_unique_index "
                "thread_id=%s turn_id=%s role=%s message_id=%s",
                thread_id,
                turn_id_value,
                message_role.value,
                db_message.id,
            )
        else:
            # INSERT succeeded — load the full ORM row so the
            # response can include server-side-defaulted columns
            # (``created_at``, etc.) and the relationship surface
            # stays consistent with the recovery path.
            inserted_row = await session.get(NewChatMessage, inserted_id)
            if inserted_row is None:
                # Should be impossible: we just inserted it in this
                # same transaction. Fail loud if it happens.
                raise HTTPException(
                    status_code=500,
                    detail="Inserted message could not be loaded.",
                ) from None
            db_message = inserted_row

        # Persist token usage if provided (for assistant messages).
        # ``cost_micros`` is the provider USD cost reported by LiteLLM,
        # forwarded by the FE through the appendMessage round-trip so
        # the historical TokenUsage row matches the credit debit applied
        # at finalize time.
        #
        # De-dup: ``finalize_assistant_turn`` may also race to write a
        # token_usage row for this same ``message_id`` (cross-session,
        # cross-shielded). Use ``INSERT ... ON CONFLICT DO NOTHING`` keyed
        # on the ``uq_token_usage_message_id`` partial unique index
        # (migration 142). The loser silently drops its insert; exactly
        # one row results regardless of which writer commits first.
        token_usage_data = raw_body.get("token_usage")
        if token_usage_data and message_role == NewChatMessageRole.ASSISTANT:
            insert_stmt = (
                pg_insert(TokenUsage)
                .values(
                    usage_type="chat",
                    prompt_tokens=token_usage_data.get("prompt_tokens", 0),
                    completion_tokens=token_usage_data.get("completion_tokens", 0),
                    total_tokens=token_usage_data.get("total_tokens", 0),
                    cost_micros=token_usage_data.get("cost_micros", 0),
                    model_breakdown=token_usage_data.get("usage"),
                    call_details=token_usage_data.get("call_details"),
                    thread_id=thread_id,
                    message_id=db_message.id,
                    workspace_id=thread.workspace_id,
                    user_id=user_uuid,
                )
                .on_conflict_do_nothing(
                    index_elements=["message_id"],
                    index_where=sa_text("message_id IS NOT NULL"),
                )
            )
            await session.execute(insert_stmt)

        await session.commit()

        # Build response manually to avoid lazy-loading the token_usage
        # relationship after commit (which would trigger MissingGreenlet).
        return NewChatMessageRead(
            id=db_message.id,
            thread_id=db_message.thread_id,
            role=db_message.role,
            content=db_message.content,
            created_at=db_message.created_at,
            author_id=db_message.author_id,
            token_usage=None,
            turn_id=db_message.turn_id,
        )

    except HTTPException:
        raise
    except IntegrityError:
        # Any IntegrityError that escaped the inline handler above
        # comes from a *different* constraint (foreign key, etc.) —
        # preserve the legacy 400 path.
        await session.rollback()
        raise HTTPException(
            status_code=400,
            detail="Database constraint violation. Please check your input data.",
        ) from None
    except OperationalError:
        await session.rollback()
        raise HTTPException(
            status_code=503, detail="Database operation failed. Please try again later."
        ) from None
    except Exception as e:
        await session.rollback()
        raise HTTPException(
            status_code=500,
            detail=f"An unexpected error occurred while appending the message: {e!s}",
        ) from None


@router.get("/threads/{thread_id}/messages", response_model=list[NewChatMessageRead])
async def list_messages(
    thread_id: int,
    skip: int = 0,
    limit: int = 100,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    List messages in a thread with pagination.

    Access is granted if:
    - User is the creator of the thread
    - Thread visibility is SEARCH_SPACE

    Requires CHATS_READ permission.
    """
    try:
        # Verify thread exists and user has access
        result = await session.execute(
            select(NewChatThread).filter(NewChatThread.id == thread_id)
        )
        thread = result.scalars().first()

        if not thread:
            raise HTTPException(status_code=404, detail="Thread not found")

        await check_permission(
            session,
            auth,
            thread.workspace_id,
            Permission.CHATS_READ.value,
            "You don't have permission to read chats in this workspace",
        )

        # Check thread-level access based on visibility
        await check_thread_access(session, thread, user)

        # Get messages
        query = (
            select(NewChatMessage)
            .options(selectinload(NewChatMessage.token_usage))
            .filter(NewChatMessage.thread_id == thread_id)
            .order_by(NewChatMessage.created_at)
            .offset(skip)
            .limit(limit)
        )

        result = await session.execute(query)
        return result.scalars().all()

    except HTTPException:
        raise
    except OperationalError:
        raise HTTPException(
            status_code=503, detail="Database operation failed. Please try again later."
        ) from None
    except Exception as e:
        raise HTTPException(
            status_code=500,
            detail=f"An unexpected error occurred while fetching messages: {e!s}",
        ) from None


# =============================================================================
# Agent Tools Endpoint
# =============================================================================


@router.get("/agent/tools", response_model=list[AgentToolInfo])
async def list_agent_tools(
    _auth: AuthContext = Depends(get_auth_context),
):
    """Return the list of built-in agent tools with their metadata.

    Hidden (WIP) tools are excluded from the response.
    """
    from app.agents.chat.multi_agent_chat.shared.tools.catalog import TOOL_CATALOG

    return [
        AgentToolInfo(
            name=t.name,
            description=t.description,
            enabled_by_default=t.enabled_by_default,
        )
        for t in TOOL_CATALOG
        if not t.hidden
    ]


# =============================================================================
# Chat Streaming Endpoint
# =============================================================================


@router.post("/new_chat")
async def handle_new_chat(
    request: NewChatRequest,
    http_request: Request,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    Stream chat responses from the deep agent.

    This endpoint handles the new chat functionality with streaming responses
    using Server-Sent Events (SSE) format compatible with Vercel AI SDK.

    Access is granted if:
    - User is the creator of the thread
    - Thread visibility is SEARCH_SPACE

    Requires CHATS_CREATE permission.
    """
    try:
        # Verify thread exists and user has permission
        result = await session.execute(
            select(NewChatThread).filter(NewChatThread.id == request.chat_id)
        )
        thread = result.scalars().first()

        if not thread:
            raise HTTPException(status_code=404, detail="Thread not found")

        await check_permission(
            session,
            auth,
            thread.workspace_id,
            Permission.CHATS_CREATE.value,
            "You don't have permission to chat in this workspace",
        )

        # Check thread-level access based on visibility
        await check_thread_access(session, thread, user)
        _raise_if_thread_busy_for_start(request.chat_id)
        from app.agents.chat.runtime.checkpointer import get_checkpointer

        await _raise_if_thread_awaiting_approval(
            request.chat_id, await get_checkpointer()
        )
        filesystem_selection = _resolve_filesystem_selection(
            mode=request.filesystem_mode,
            client_platform=request.client_platform,
            local_mounts=request.local_filesystem_mounts,
        )

        # Get workspace to check LLM config preferences
        workspace_result = await session.execute(
            select(Workspace).filter(Workspace.id == request.workspace_id)
        )
        workspace = workspace_result.scalars().first()

        if not workspace:
            raise HTTPException(status_code=404, detail="Workspace not found")

        # Use the converged model-connections role for chat operations.
        # Positive IDs load Model + Connection rows; negative IDs load
        # virtual GLOBAL models; 0 means Auto.
        llm_config_id = (
            workspace.chat_model_id if workspace.chat_model_id is not None else 0
        )

        # Release the read-transaction so we don't hold ACCESS SHARE locks
        # on workspaces/documents for the entire duration of the stream.
        # expire_on_commit=False keeps loaded ORM attrs usable.
        await session.commit()
        # Close the dependency session now so its connection returns to
        # the pool before streaming begins.  Without this, Starlette's
        # BaseHTTPMiddleware cancels the scope on client disconnect and
        # the dependency generator's __aexit__ never runs, orphaning the
        # connection (the "Exception terminating connection" errors).
        await session.close()

        image_urls = (
            [p.as_data_url() for p in request.user_images]
            if request.user_images
            else None
        )

        mentioned_documents_payload = (
            [doc.model_dump() for doc in request.mentioned_documents]
            if request.mentioned_documents
            else None
        )
        mentioned_connectors_payload = (
            [doc.model_dump() for doc in request.mentioned_connectors]
            if request.mentioned_connectors
            else None
        )

        # Checked again with no await before the turn registers: two sends
        # racing through the awaits above must not both start a turn.
        _raise_if_thread_busy_for_start(request.chat_id)
        return StreamingResponse(
            start_turn_in_background(
                request.chat_id,
                stream_new_chat(
                    user_query=request.user_query,
                    workspace_id=request.workspace_id,
                    chat_id=request.chat_id,
                    user_id=str(user.id),
                    llm_config_id=llm_config_id,
                    mentioned_document_ids=request.mentioned_document_ids,
                    mentioned_folder_ids=request.mentioned_folder_ids,
                    mentioned_connector_ids=request.mentioned_connector_ids,
                    mentioned_connectors=mentioned_connectors_payload,
                    mentioned_documents=mentioned_documents_payload,
                    mentioned_thread_ids=request.mentioned_thread_ids,
                    needs_history_bootstrap=thread.needs_history_bootstrap,
                    thread_visibility=thread.visibility,
                    current_user_display_name=user.display_name or "A team member",
                    disabled_tools=request.disabled_tools,
                    retrieval_scope=request.retrieval_scope,
                    preferred_tool=request.preferred_tool,
                    filesystem_selection=filesystem_selection,
                    request_id=getattr(http_request.state, "request_id", "unknown"),
                    user_image_data_urls=image_urls,
                    auth_context=auth,
                ),
                on_end=push_events.turn_end_hook(
                    thread_id=request.chat_id,
                    user_id=str(user.id),
                    notebook_id=thread.notebook_id,
                ),
            ),
            media_type="text/event-stream",
            headers={
                "Cache-Control": "no-cache",
                "Connection": "keep-alive",
                "X-Accel-Buffering": "no",
            },
        )

    except HTTPException:
        raise
    except Exception as e:
        raise HTTPException(
            status_code=500,
            detail=f"An unexpected error occurred: {e!s}",
        ) from None


@router.post(
    "/threads/{thread_id}/cancel-active-turn",
    response_model=CancelActiveTurnResponse,
)
async def cancel_active_turn(
    thread_id: int,
    response: Response,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """Signal cancellation for the currently running turn on ``thread_id``."""
    result = await session.execute(
        select(NewChatThread).filter(NewChatThread.id == thread_id)
    )
    thread = result.scalars().first()
    if not thread:
        raise HTTPException(status_code=404, detail="Thread not found")

    await check_permission(
        session,
        auth,
        thread.workspace_id,
        Permission.CHATS_UPDATE.value,
        "You don't have permission to update chats in this workspace",
    )
    await check_thread_access(session, thread, user)

    status_payload = _build_turn_status_payload(thread_id)
    if status_payload["status"] == "idle":
        return CancelActiveTurnResponse(
            status="idle",
            error_code="NO_ACTIVE_TURN",
            message=chat_error_message("NO_ACTIVE_TURN"),
        )

    if status_payload["status"] == "cancelling":
        # Already stopping: a second press, or Stop from another page. Another
        # cancel could only reach the turn's end-of-turn work, so this one
        # changes nothing; it waits for the same save and answers the same.
        await wait_for_running_turn(str(thread_id), wait_seconds=STOP_WAIT_SECONDS)
    else:
        request_cancel(str(thread_id))
        # The turn runs in its own task (it outlives the page that started
        # it), so Stop must cancel that task; the cancel flag alone is
        # advisory.
        await stop_running_turn(str(thread_id), wait_seconds=STOP_WAIT_SECONDS)
    response.status_code = 202
    updated_payload = _build_turn_status_payload(thread_id)
    retry_after_ms = int(updated_payload.get("retry_after_ms") or 0)
    retry_after_at = (
        int(updated_payload["retry_after_at"])
        if "retry_after_at" in updated_payload
        else None
    )
    if retry_after_ms > 0:
        _set_retry_after_headers(response, retry_after_ms)
    return CancelActiveTurnResponse(
        status="cancelling",
        error_code="TURN_CANCELLING",
        message=chat_error_message("TURN_CANCELLING"),
        retry_after_ms=retry_after_ms if retry_after_ms > 0 else None,
        retry_after_at=retry_after_at,
    )


@router.get(
    "/threads/{thread_id}/turn-status",
    response_model=TurnStatusResponse,
)
async def get_turn_status(
    thread_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    result = await session.execute(
        select(NewChatThread).filter(NewChatThread.id == thread_id)
    )
    thread = result.scalars().first()
    if not thread:
        raise HTTPException(status_code=404, detail="Thread not found")

    await check_permission(
        session,
        auth,
        thread.workspace_id,
        Permission.CHATS_READ.value,
        "You don't have permission to view chats in this workspace",
    )
    await check_thread_access(session, thread, user)

    status_payload = _build_turn_status_payload(thread_id)
    return TurnStatusResponse(
        status=status_payload["status"],  # type: ignore[arg-type]
        active_turn_id=None,
        retry_after_ms=status_payload.get("retry_after_ms"),  # type: ignore[arg-type]
        retry_after_at=status_payload.get("retry_after_at"),  # type: ignore[arg-type]
    )


# =============================================================================
# Chat Regeneration Endpoint (Edit/Reload)
# =============================================================================


@router.post("/threads/{thread_id}/regenerate")
async def regenerate_response(
    thread_id: int,
    request: RegenerateRequest,
    http_request: Request,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    """
    Ask a turn of a chat again (Regenerate), or ask it reworded (Edit).

    1. **Edit**: ``user_query`` (and ``user_images``) replace the question.
    2. **Regenerate**: no ``user_query``; the saved question is asked again.

    The replaced turn is the one ``from_message_id`` belongs to (a question or
    an answer), or the latest one. Both operations:

    - re-run from the agent's memory as it stood before that question, so the
      old answer is not in it (``flows/regenerate/rewind_point.py``);
    - ask with the tool the question was asked with (Tools menu);
    - delete the replaced messages once the re-run has moved the agent's
      memory, keeping their token usage.

    Access is granted if:
    - User is the creator of the thread
    - Thread visibility is SEARCH_SPACE

    Requires CHATS_UPDATE permission.
    """
    from app.agents.chat.runtime.checkpointer import get_checkpointer

    try:
        # Verify thread exists and user has permission
        result = await session.execute(
            select(NewChatThread).filter(NewChatThread.id == thread_id)
        )
        thread = result.scalars().first()

        if not thread:
            raise HTTPException(status_code=404, detail="Thread not found")

        await check_permission(
            session,
            auth,
            thread.workspace_id,
            Permission.CHATS_UPDATE.value,
            "You don't have permission to update chats in this workspace",
        )

        # Check thread-level access based on visibility
        await check_thread_access(session, thread, user)
        _raise_if_thread_busy_for_start(thread_id)

        checkpointer = await get_checkpointer()
        await _raise_if_thread_awaiting_approval(thread_id, checkpointer)
        filesystem_selection = _resolve_filesystem_selection(
            mode=request.filesystem_mode,
            client_platform=request.client_platform,
            local_mounts=request.local_filesystem_mounts,
        )

        try:
            replaced = await find_replaced_turn(
                session, thread_id=thread_id, from_message_id=request.from_message_id
            )
        except FromMessageNotInThreadError:
            raise HTTPException(
                status_code=404,
                detail="from_message_id not found in this thread.",
            ) from None
        if replaced is None:
            raise HTTPException(
                status_code=400,
                detail="Could not determine user query for regeneration. Please provide a user_query.",
            )
        rewind = await find_rewind_point(
            checkpointer, thread_id=thread_id, replaced=replaced
        )

        is_edit = request.user_query is not None
        user_query_to_use = request.user_query if is_edit else replaced.user_query
        if request.user_images is not None:
            regenerate_image_urls = [p.as_data_url() for p in request.user_images]
        else:
            regenerate_image_urls = [] if is_edit else list(replaced.image_urls)
        if not str(user_query_to_use).strip() and not regenerate_image_urls:
            raise HTTPException(
                status_code=400,
                detail="Could not determine user query for regeneration. Please provide a user_query.",
            )

        # The Tools menu pick belongs to the question, and the page forgot it
        # when the question was sent, so it comes from the turn's checkpoints.
        preferred_tool = rewind.preferred_tool
        retrieval_scope = scope_for_tool_preference(
            request.retrieval_scope, preferred_tool
        )

        # When revert_actions is requested, every replaced turn is reverted
        # (best-effort) BEFORE the regenerate stream begins. Rows saved
        # before turn ids existed have nothing to walk.
        revert_turn_ids: list[str] = []
        if request.revert_actions:
            if replaced.turn_id is None:
                _logger.warning(
                    "[regenerate] message=%s on thread=%s has no turn_id; "
                    "revert_actions ignored.",
                    replaced.user_message_id,
                    thread_id,
                )
            else:
                revert_turn_ids = list(replaced.turn_ids)

        # Get workspace for LLM config
        workspace_result = await session.execute(
            select(Workspace).filter(Workspace.id == request.workspace_id)
        )
        workspace = workspace_result.scalars().first()

        if not workspace:
            raise HTTPException(status_code=404, detail="Workspace not found")

        llm_config_id = (
            workspace.chat_model_id if workspace.chat_model_id is not None else 0
        )

        # Release the read-transaction so we don't hold ACCESS SHARE locks
        # on workspaces/documents for the entire duration of the stream.
        # expire_on_commit=False keeps loaded ORM attrs usable.
        await session.commit()
        await session.close()

        async def stream_with_cleanup():
            try:
                # Best-effort revert pass BEFORE the regenerate stream begins.
                # Each turn is reverted independently (per-row SAVEPOINTs
                # inside the route helper) and the per-action results are
                # surfaced on a single ``data-revert-results`` SSE event so the
                # frontend can render any failed rows alongside the new turn.
                # Failures here do NOT abort the regeneration — partial
                # rollback is documented behaviour.
                if revert_turn_ids:
                    revert_results = await _revert_turns_for_regenerate(
                        thread_id=thread_id,
                        chat_turn_ids=revert_turn_ids,
                        requester_user_id=str(user.id),
                    )
                    envelope = {
                        "type": "data-revert-results",
                        "data": revert_results,
                    }
                    yield f"data: {json.dumps(envelope, default=str)}\n\n".encode()
                mentioned_documents_payload = (
                    [doc.model_dump() for doc in request.mentioned_documents]
                    if request.mentioned_documents
                    else None
                )
                mentioned_connectors_payload = (
                    [doc.model_dump() for doc in request.mentioned_connectors]
                    if request.mentioned_connectors
                    else None
                )
                async for chunk in stream_new_chat(
                    user_query=str(user_query_to_use),
                    workspace_id=request.workspace_id,
                    chat_id=thread_id,
                    user_id=str(user.id),
                    llm_config_id=llm_config_id,
                    mentioned_document_ids=request.mentioned_document_ids,
                    mentioned_folder_ids=request.mentioned_folder_ids,
                    mentioned_connector_ids=request.mentioned_connector_ids,
                    mentioned_connectors=mentioned_connectors_payload,
                    mentioned_documents=mentioned_documents_payload,
                    mentioned_thread_ids=request.mentioned_thread_ids,
                    checkpoint_id=rewind.checkpoint_id,
                    needs_history_bootstrap=(
                        thread.needs_history_bootstrap or rewind.rebuild_history
                    ),
                    history_before=replaced.starts_at,
                    thread_visibility=thread.visibility,
                    current_user_display_name=user.display_name or "A team member",
                    disabled_tools=request.disabled_tools,
                    retrieval_scope=retrieval_scope,
                    preferred_tool=preferred_tool,
                    filesystem_selection=filesystem_selection,
                    request_id=getattr(http_request.state, "request_id", "unknown"),
                    user_image_data_urls=regenerate_image_urls or None,
                    auth_context=auth,
                    flow="regenerate",
                ):
                    yield chunk
            finally:
                # Also after a Stop or an error: once the re-run has moved the
                # agent's memory, the replaced rows must go so the chat and
                # the memory agree. A Stop that lands now (the reply is
                # already saved) must not cut the delete short and leave both
                # replies behind.
                await run_cleanup_to_the_end(
                    remove_replaced_messages(
                        thread_id,
                        replaced.message_ids,
                        latest_before_rerun=rewind.latest_checkpoint_id,
                    )
                )

        # Checked again with no await before the turn registers: two sends
        # racing through the awaits above must not both start a turn.
        _raise_if_thread_busy_for_start(thread_id)
        return StreamingResponse(
            start_turn_in_background(
                thread_id,
                stream_with_cleanup(),
                on_end=push_events.turn_end_hook(
                    thread_id=thread_id,
                    user_id=str(user.id),
                    notebook_id=thread.notebook_id,
                ),
            ),
            media_type="text/event-stream",
            headers={
                "Cache-Control": "no-cache",
                "Connection": "keep-alive",
                "X-Accel-Buffering": "no",
            },
        )

    except HTTPException:
        raise
    except Exception as e:
        import traceback

        traceback.print_exc()
        raise HTTPException(
            status_code=500,
            detail=f"An unexpected error occurred during regeneration: {e!s}",
        ) from None


# =============================================================================
# Resume Interrupted Chat Endpoint
# =============================================================================


@router.get(
    "/threads/{thread_id}/pending-interrupts",
    response_model=PendingInterruptsResponse,
)
async def get_pending_interrupts(
    thread_id: int,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    """Return the thread's paused HITL interrupts, if any.

    The live approval card lives only in the frontend's in-memory stream
    overlay, so a page refresh loses it while the LangGraph checkpoint stays
    paused. The frontend calls this on thread load to re-render the card and
    let the user resume or reject.
    """
    from app.agents.chat.runtime.checkpointer import get_checkpointer
    from app.services.new_streaming_service import VercelStreamingService
    from app.tasks.chat.persistence import load_assistant_message_for_turn
    from app.tasks.chat.streaming.helpers.interrupt_inspector import (
        pending_interrupt_entries_from_writes,
    )

    result = await session.execute(
        select(NewChatThread).filter(NewChatThread.id == thread_id)
    )
    thread = result.scalars().first()
    if not thread:
        raise HTTPException(status_code=404, detail="Thread not found")

    await check_permission(
        session,
        auth,
        thread.workspace_id,
        Permission.CHATS_READ.value,
        "You don't have permission to read chats in this workspace",
    )
    await check_thread_access(session, thread, user=auth.user)

    checkpointer = await get_checkpointer()
    checkpoint_tuple = await checkpointer.aget_tuple(
        {"configurable": {"thread_id": str(thread_id)}}
    )
    if checkpoint_tuple is None:
        return PendingInterruptsResponse()

    entries = pending_interrupt_entries_from_writes(checkpoint_tuple.pending_writes)
    if not entries:
        return PendingInterruptsResponse()

    service = VercelStreamingService()
    payloads: list[dict] = []
    for value, interrupt_id in entries:
        payload = service._normalize_interrupt_payload(value)
        if interrupt_id is not None:
            payload = {**payload, "interrupt_id": interrupt_id}
        payloads.append(payload)

    # Reattach the card only to the paused turn's assistant row. ``turn_id`` on
    # the checkpoint mirrors ``NewChatMessage.turn_id``.
    metadata = checkpoint_tuple.metadata or {}
    turn_id = metadata.get("turn_id") if isinstance(metadata, dict) else None
    assistant_message = await load_assistant_message_for_turn(
        session,
        chat_id=thread_id,
        turn_id=turn_id if isinstance(turn_id, str) else None,
    )

    return PendingInterruptsResponse(
        assistant_message_id=assistant_message.id if assistant_message else None,
        pending_interrupts=payloads,
    )


@router.post("/threads/{thread_id}/resume")
async def resume_chat(
    thread_id: int,
    request: ResumeRequest,
    http_request: Request,
    session: AsyncSession = Depends(get_async_session),
    auth: AuthContext = Depends(get_auth_context),
):
    user = auth.user
    try:
        result = await session.execute(
            select(NewChatThread).filter(NewChatThread.id == thread_id)
        )
        thread = result.scalars().first()

        if not thread:
            raise HTTPException(status_code=404, detail="Thread not found")

        await check_permission(
            session,
            auth,
            thread.workspace_id,
            Permission.CHATS_CREATE.value,
            "You don't have permission to chat in this workspace",
        )

        await check_thread_access(session, thread, user)
        _raise_if_thread_busy_for_start(thread_id)
        filesystem_selection = _resolve_filesystem_selection(
            mode=request.filesystem_mode,
            client_platform=request.client_platform,
            local_mounts=request.local_filesystem_mounts,
        )

        workspace_result = await session.execute(
            select(Workspace).filter(Workspace.id == request.workspace_id)
        )
        workspace = workspace_result.scalars().first()

        if not workspace:
            raise HTTPException(status_code=404, detail="Workspace not found")

        llm_config_id = (
            workspace.chat_model_id if workspace.chat_model_id is not None else 0
        )

        decisions = [d.model_dump() for d in request.decisions]

        # Release the read-transaction so we don't hold ACCESS SHARE locks
        # on workspaces/documents for the entire duration of the stream.
        await session.commit()
        await session.close()

        # Checked again with no await before the turn registers: two sends
        # racing through the awaits above must not both start a turn.
        _raise_if_thread_busy_for_start(thread_id)
        return StreamingResponse(
            start_turn_in_background(
                thread_id,
                stream_resume_chat(
                    chat_id=thread_id,
                    workspace_id=request.workspace_id,
                    decisions=decisions,
                    user_id=str(user.id),
                    llm_config_id=llm_config_id,
                    thread_visibility=thread.visibility,
                    filesystem_selection=filesystem_selection,
                    request_id=getattr(http_request.state, "request_id", "unknown"),
                    disabled_tools=request.disabled_tools,
                    retrieval_scope=request.retrieval_scope,
                    auth_context=auth,
                ),
                on_end=push_events.turn_end_hook(
                    thread_id=thread_id,
                    user_id=str(user.id),
                    notebook_id=thread.notebook_id,
                ),
            ),
            media_type="text/event-stream",
            headers={
                "Cache-Control": "no-cache",
                "Connection": "keep-alive",
                "X-Accel-Buffering": "no",
            },
        )

    except HTTPException:
        raise
    except Exception as e:
        import traceback

        traceback.print_exc()
        raise HTTPException(
            status_code=500,
            detail=f"An unexpected error occurred during resume: {e!s}",
        ) from None
