"""What the rest of the app calls when something finished (the plan's §6 events).

Each function is synchronous, returns nothing, and never raises: it hands a
small payload to the Celery task (``tasks.py``) that does the lookups and the
sending. Importing this module pulls in no database or Celery code, so the
chat relay and the notification handlers can call it freely. Nothing here
runs unless ``PUSH_ENABLED`` is on.

The one rule with a decision in it, the chat turn's, is ``turn_rule``: pure,
so it is tested on its own. Every limit is in
``custom/docs/push-notifications.md``.
"""

from __future__ import annotations

import logging
from typing import Any
from uuid import UUID

from app.push.kinds import PushKind
from app.push.message import notebook_path

logger = logging.getLogger(__name__)

# An answer that finished while no page was listening is pushed only when the
# turn took at least this long: a quick answer the person just stepped away
# from is not worth a buzz (plan §6; custom/docs/push-notifications.md).
ANSWER_MIN_SECONDS = 20.0


def turn_rule(
    *,
    attached: bool,
    seconds: float,
    failed: bool,
    cancelled: bool,
    interrupted: bool,
) -> list[tuple[PushKind, bool]]:
    """Which pushes a finished chat turn earns, as (kind, failed) pairs.

    - Stop (``cancelled``) earns nothing: the person did it.
    - A turn that paused for an approval earns "approval" when no page was
      listening (a page that was shows the approval card itself).
    - Otherwise "answer" (ready or failed), only when nobody was listening and
      the turn ran at least ``ANSWER_MIN_SECONDS``.
    """
    if cancelled or attached:
        return []
    if interrupted:
        return [("approval", False)]
    if seconds < ANSWER_MIN_SECONDS:
        return []
    return [("answer", failed)]


def _enqueue(payload: dict[str, Any]) -> None:
    try:
        from app.config import config

        if not config.PUSH_ENABLED:
            return
        from app.push.tasks import push_send_task

        push_send_task.delay(payload)
    except Exception as exc:  # never let a push break the thing it reports on
        logger.warning("push: could not queue %s: %s", payload.get("kind"), exc)


def _payload(
    kind: PushKind,
    user_id: UUID | str | None,
    *,
    failed: bool = False,
    path: str | None = None,
    title: str | None = None,
    output_kind: str | None = None,
    thread_id: int | None = None,
    variant: str | None = None,
    workspace_id: int | None = None,
) -> dict[str, Any]:
    return {
        "kind": kind,
        "user_id": str(user_id) if user_id else None,
        "failed": failed,
        "path": path,
        "title": title,
        "output_kind": output_kind,
        "thread_id": thread_id,
        "variant": variant,
        # When None, the task looks it up from the thread, notebook or automation.
        "workspace_id": workspace_id,
    }


def turn_ended(
    *,
    thread_id: int | str,
    user_id: UUID | str | None,
    notebook_id: int | None,
    attached: bool,
    seconds: float,
    failed: bool,
    cancelled: bool,
    interrupted: bool,
) -> None:
    """A chat turn ended (``detached_turn/relay.py``).

    No title travels with it: a chat's title is written from what was asked.
    With ``PUSH_INCLUDE_TITLES`` on, the task adds the notebook's name
    (``tasks._resolve_title``), as for every push that opens a notebook.
    """
    for kind, did_fail in turn_rule(
        attached=attached,
        seconds=seconds,
        failed=failed,
        cancelled=cancelled,
        interrupted=interrupted,
    ):
        _enqueue(
            _payload(
                kind,
                user_id,
                failed=did_fail,
                path=notebook_path(notebook_id, "chat"),
                thread_id=int(thread_id) if str(thread_id).isdigit() else None,
            )
        )


def turn_end_hook(
    *,
    thread_id: int | str,
    user_id: UUID | str | None,
    notebook_id: int | None,
):
    """The ``on_end`` a chat route hands ``start_turn_in_background``.

    Bound to the thread, the person who asked and the notebook, it turns the
    relay's ``TurnEnd`` into ``turn_ended``.
    """

    def on_end(end) -> None:
        turn_ended(
            thread_id=thread_id,
            user_id=user_id,
            notebook_id=notebook_id,
            attached=end.attached,
            seconds=end.seconds,
            failed=end.failed,
            cancelled=end.cancelled,
            interrupted=end.interrupted,
        )

    return on_end


def output_ended(
    *,
    user_id: UUID | str | None,
    thread_id: int | None,
    output_kind: str,
    failed: bool,
) -> None:
    """A Studio output is ready or failed (deliverable jobs, podcasts, videos).

    The notebook comes from the thread when the task runs; ``user_id`` may be
    None for podcasts and videos, whose rows name only the thread. No title
    travels with it (an output's is written from its sources); with
    ``PUSH_INCLUDE_TITLES`` on, the task adds the notebook's name.
    """
    _enqueue(
        _payload(
            "output",
            user_id,
            failed=failed,
            output_kind=output_kind,
            thread_id=thread_id,
        )
    )


def automation_run_ended(
    *,
    owner_user_id: UUID | str | None,
    automation_id: int,
    run_id: int,
    status: str,
) -> None:
    """A run reached a terminal status other than cancelled (``executor.py``, ``overdue.py``)."""
    if not owner_user_id or status == "cancelled":
        return
    _enqueue(
        _payload(
            "automation",
            owner_user_id,
            failed=status != "succeeded",
            path=f"/automation/{automation_id}/runs/{run_id}",
        )
    )


def source_ended(*, user_id: UUID | str, failed: bool, connector: bool = False) -> None:
    """A document finished processing, or could not be; or a connector sync ended.

    Called by the ``document_processing`` and ``connector_indexing`` handlers.
    """
    _enqueue(
        _payload(
            "source",
            user_id,
            failed=failed,
            path="/inbox",
            variant="connector" if connector else None,
        )
    )


def mentioned(
    *, user_id: UUID | str, notebook_id: int | None, reply: bool = False
) -> None:
    """A mention or a reply (the comment handlers)."""
    _enqueue(
        _payload(
            "mention",
            user_id,
            path=notebook_path(notebook_id, "chat"),
            variant="reply" if reply else None,
        )
    )


def announced(user_ids: list[UUID], *, title: str | None = None) -> None:
    """An administrator sent an announcement: one push per recipient."""
    for user_id in dict.fromkeys(user_ids):
        _enqueue(
            _payload(
                "announcement", user_id, path="/inbox?tab=announcements", title=title
            )
        )


def invite_accepted(
    *, inviter_user_id: UUID | str | None, workspace_id: int | None = None
) -> None:
    """Someone joined through an invite (``rbac_routes.accept_invite``).

    ``workspace_id`` is the invite's, so the tap opens that workspace's Members.
    """
    if not inviter_user_id:
        return
    _enqueue(
        _payload(
            "invite",
            inviter_user_id,
            path="/workspace/members",
            workspace_id=workspace_id,
        )
    )


def credits_out(*, user_id: UUID | str) -> None:
    """Processing stopped for want of credits (``insufficient_credits``)."""
    _enqueue(_payload("credits", user_id, path="/you/plan"))
