import logging
from datetime import date
from typing import Any

from langchain_core.tools import tool
from sqlalchemy.ext.asyncio import AsyncSession

from app.connectors.email_imap.client import MAX_SEARCH_RESULTS
from app.connectors.email_imap.search_results import more_than_shown
from app.connectors.email_imap.service import (
    EmailAccountNotFoundError,
    search_across_accounts as _search,
)

logger = logging.getLogger(__name__)


def create_search_emails_tool(
    db_session: AsyncSession | None = None,
    workspace_id: int | None = None,
    user_id: str | None = None,
):
    @tool
    async def search_emails(
        text: str | None = None,
        from_: str | None = None,
        to: str | None = None,
        subject: str | None = None,
        since: str | None = None,
        before: str | None = None,
        account_id: str | None = None,
        folder: str = "INBOX",
        unread_only: bool = False,
        limit: int = 20,
    ) -> dict[str, Any]:
        """Search the user's connected mailbox(es), read-only.

        Searches one account if `account_id` is given, otherwise every
        connected account (each result is labelled with its account).

        Args:
            text: Free text to search for anywhere in the message.
            from_: Sender address or name to match.
            to: Recipient address to match.
            subject: Subject-line text to match.
            since: Only messages on/after this date, "YYYY-MM-DD".
            before: Only messages before this date, "YYYY-MM-DD".
            account_id: Restrict to one account (from `list_email_accounts`); omit to search all.
            folder: Mailbox folder to search (default "INBOX").
            unread_only: Only messages not yet read in the user's mail app
                (IMAP UNSEEN). Set it whenever the user asks about unread mail.
            limit: Max messages across all accounts, newest first, capped at 50.

        Returns:
            {"status": "success", "truncated": bool, "results": [
                {"account_label", "folder", "from", "to", "subject", "date", "uid", "account_id"}
                or {"account_label", "error"} for an account that couldn't be searched
            ]}
            When "truncated" is true more messages matched than were returned
            (see "message"); say so, and narrow the dates or search one
            account at a time rather than presenting the list as complete.
        """
        if db_session is None or workspace_id is None or user_id is None:
            return {
                "status": "error",
                "message": "Email connector not properly configured.",
            }

        def _parse_date(value: str | None) -> date | None:
            if not value:
                return None
            try:
                return date.fromisoformat(value)
            except ValueError:
                return None

        # Ask for one more than shown: its presence is how "truncated" is known.
        shown = max(1, min(limit, MAX_SEARCH_RESULTS))
        try:
            results = await _search(
                db_session,
                workspace_id=workspace_id,
                user_id=user_id,
                account_id=account_id,
                folder=folder,
                text=text,
                from_=from_,
                to=to,
                subject=subject,
                since=_parse_date(since),
                before=_parse_date(before),
                limit=shown + 1,
                for_workspace_use=True,
                unread_only=unread_only,
            )
            if not results:
                return {
                    "status": "blocked",
                    "results": [],
                    "message": "No connected email accounts or no matches.",
                }
            errors = [row for row in results if "error" in row]
            hits = [row for row in results if "error" not in row]
            truncated = more_than_shown(
                hits, limit=shown, per_account_cap=MAX_SEARCH_RESULTS
            )
            reply: dict[str, Any] = {
                "status": "success",
                "truncated": truncated,
                "results": errors + hits[:shown],
            }
            if truncated:
                reply["message"] = (
                    f"More messages match than the {shown} shown (newest first). "
                    "Narrow the dates or search one account at a time to see the rest."
                )
            return reply
        except EmailAccountNotFoundError:
            return {
                "status": "not_found",
                "message": "That email account was not found.",
            }
        except Exception as exc:
            logger.error("search_emails failed: %s", exc, exc_info=True)
            return {"status": "error", "message": "Email search failed."}

    return search_emails
