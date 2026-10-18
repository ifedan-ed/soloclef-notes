"""Where the browser sheet goes back to when a hand-off's flow ends.

Always the app's own address (``APP_HANDOFF_RETURN``, by default
``soloclef://connectors/done``), never one a request names: the server picks
it, so a forged hand-off cannot send anything elsewhere. The app's
``openAuthSessionAsync`` waits for that address and reads ``purpose`` and
``status`` from it (soloclef_mobile/src/features/workspace/connectors/
handoff.ts words each status).
"""

from __future__ import annotations

from urllib.parse import urlencode

DEFAULT_RETURN = "soloclef://connectors/done"

#: A code that was never issued, was spent already or ran out.
EXPIRED = "expired"
#: The server cannot run this flow (its provider is not set up).
NOT_CONFIGURED = "not_configured"
#: The provider said yes, and nothing is saved yet: the app confirms with
#: ``POST /api/v1/app/handoff/complete {completion, code_verifier}`` (its
#: bearer token, the verifier behind its challenge). Comes with ``completion``.
CONFIRM = "confirm"


def app_return(
    purpose: str | None,
    status: str,
    base: str | None = None,
    *,
    completion: str | None = None,
) -> str:
    """The app's return address with the flow's purpose and how it ended."""
    query = {"purpose": purpose, "status": status} if purpose else {"status": status}
    if completion:
        query["completion"] = completion
    return f"{(base or DEFAULT_RETURN)}?{urlencode(query)}"


def configured_return() -> str:
    from app.config import config

    return getattr(config, "APP_HANDOFF_RETURN", None) or DEFAULT_RETURN
