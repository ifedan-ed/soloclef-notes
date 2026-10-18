"""How a Microsoft connect ended: the ``microsoft_mail`` value on the page it returns to.

The connectors page turns each into one short message
(``surfsense_web/features/email-imap-connect/lib/microsoft-mail-outcome.ts``).
"""

from __future__ import annotations

QUERY_PARAM = "microsoft_mail"

CONNECTED = "connected"
#: The person cancelled or declined on Microsoft's page.
DENIED = "denied"
#: No connect in progress in this browser: the 10-minute window passed, the
#: callback was opened elsewhere, or its state was not this browser's.
EXPIRED = "expired"
#: A work or school account whose organization only lets admins consent.
ADMIN_APPROVAL = "admin_approval"
#: The person unticked mailbox access on the consent page.
SCOPE_MISSING = "scope_missing"
#: Microsoft issued no refresh token (offline access refused).
NO_REFRESH_TOKEN = "no_refresh_token"
#: The account has no email address to sign in to IMAP as.
NO_ADDRESS = "no_address"
FAILED = "failed"
NOT_CONFIGURED = "not_configured"


class ConnectRefusedError(Exception):
    """A connect that cannot finish; ``outcome`` is what the page is told."""

    def __init__(self, outcome: str, reason: str) -> None:
        super().__init__(reason)
        self.outcome = outcome
