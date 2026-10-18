"""Send an automation run's result email, never to anyone but the owner.

Called by the executor when a run ends, and by the overdue sweep when it
times out a run whose worker was lost (``runtime/overdue.py``) -- in both
cases only by whoever wrote the run's terminal status, so at most once per
run: the same cadence the run itself fires at, since nothing here re-sends or
batches. Never sent for a cancelled run. Only when the automation's
``execution.email_result`` is on (off by default); "owner" is the account that
created the automation, even when someone else switched the email on.

Every outcome is logged (sent, mail off, no address, failed) so a missing
email can be traced from the worker log; the address itself never is.
"""

from __future__ import annotations

import logging
from datetime import UTC, datetime
from typing import Any
from uuid import UUID

from sqlalchemy.ext.asyncio import AsyncSession

from app.db import User
from app.mailer import MailerError, get_mailer, is_mail_enabled

from .message import build_failure_email, build_success_email

logger = logging.getLogger(__name__)


def _final_message(result: dict[str, Any] | None) -> str | None:
    if not isinstance(result, dict):
        return None
    message = result.get("final_message")
    return message if isinstance(message, str) and message.strip() else None


def _error_reason(error: dict[str, Any] | None) -> str | None:
    if not isinstance(error, dict):
        return None
    message = error.get("message")
    return message if isinstance(message, str) and message.strip() else None


async def send_run_result_email(
    session: AsyncSession,
    *,
    owner_user_id: UUID | None,
    automation_id: int | None = None,
    automation_name: str,
    status: str,
    result: dict[str, Any] | None = None,
    error: dict[str, Any] | None = None,
) -> None:
    """``status`` is ``succeeded``, ``failed``, ``needs_attention`` or ``timed_out``.

    ``succeeded`` sends the run's final answer; the others send the short
    email whose subject ends "(failed)", "(needs attention)" or "(timed out)"
    with the reason from ``error``. Never called for ``cancelled``.

    Does nothing (but logs why) when mail is off, the owner account is gone,
    or sending fails -- a notification is best-effort, never worth failing (or
    re-failing) the run over. The run is already committed by the time this
    runs, so nothing may escape from here.
    """
    if not owner_user_id:
        return
    if not is_mail_enabled():
        logger.warning(
            "Automation %s result email for user %s not sent: SMTP is off",
            automation_id,
            owner_user_id,
        )
        return
    owner = await session.get(User, owner_user_id)
    if owner is None or not owner.email:
        logger.warning(
            "Automation %s result email not sent: creator account %s has no email",
            automation_id,
            owner_user_id,
        )
        return

    try:
        run_date = datetime.now(UTC)
        if status == "succeeded":
            message = build_success_email(
                to=owner.email,
                automation_name=automation_name,
                run_date=run_date,
                output_text=_final_message(result),
            )
        else:
            message = build_failure_email(
                to=owner.email,
                automation_name=automation_name,
                run_date=run_date,
                status=status,
                reason=_error_reason(error),
            )
        await get_mailer().send(message)
    except MailerError as exc:
        logger.error(
            "Automation %s result email to user %s failed: %s",
            automation_id,
            owner_user_id,
            type(exc).__name__,
        )
        return
    except Exception as exc:
        # SmtpMailer() raises ValueError when SMTP_HOST / SMTP_FROM /
        # SMTP_SECURITY is wrong; those messages name the setting, not a secret.
        logger.error(
            "Automation %s result email to user %s could not be sent: %s: %s",
            automation_id,
            owner_user_id,
            type(exc).__name__,
            exc,
        )
        return
    logger.info(
        "Automation %s result email (%s) sent to user %s",
        automation_id,
        status,
        owner_user_id,
    )
