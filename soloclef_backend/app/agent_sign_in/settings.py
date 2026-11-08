"""What agent sign-in needs from ``docker/.env``.

Every value here is documented in ``custom/README.md`` ("AI agents sign in
with Authentik"). The client secret is only handed to signed-in people in the
"Connect your agent" dialog, never logged or echoed.
"""

from __future__ import annotations

import logging
import os
from collections.abc import Mapping
from dataclasses import dataclass

logger = logging.getLogger(__name__)

_WELL_KNOWN = ".well-known/openid-configuration"


@dataclass(frozen=True)
class AgentSignInSettings:
    # Exactly as Authentik prints it, trailing slash included, e.g.
    # https://sso.example.com/application/o/soloclef-mcp/
    issuer: str
    # The Authentik provider's client id: an access token is accepted only
    # when its audience is this client, so a token Authentik issued to any
    # other application (the web sign-in included) is refused.
    client_id: str
    # Shown in "Connect your agent" so people can paste it into claude.ai and
    # ChatGPT; Notes itself never uses it.
    client_secret: str = ""
    # The ``oauth_account.oauth_name`` the web sign-in links identities under
    # (``OIDC_PROVIDER_NAME``), so an identity linked there is found here.
    provider_name: str = "authentik"
    # Who may connect is not a setting: it is whoever Authentik lets into the
    # notes application, read from each token (``NOTES_ACCESS_CLAIM`` in
    # ``access_token.py``). ``MCP_OAUTH_REQUIRED_GROUP`` (a group check of
    # the first, never deployed version) is no longer read; a warning says so.

    def __repr__(self) -> str:  # keep the secret out of tracebacks and logs
        return (
            f"AgentSignInSettings(issuer={self.issuer!r}, client_id={self.client_id!r})"
        )

    @property
    def enabled(self) -> bool:
        return bool(self.issuer and self.client_id)

    @property
    def discovery_url(self) -> str:
        return f"{self.issuer.rstrip('/')}/{_WELL_KNOWN}"


def load_agent_sign_in_settings(
    env: Mapping[str, str] | None = None,
) -> AgentSignInSettings:
    env = os.environ if env is None else env
    if (env.get("MCP_OAUTH_REQUIRED_GROUP") or "").strip():
        logger.warning(
            "MCP_OAUTH_REQUIRED_GROUP is no longer read: every Notes account "
            "Authentik lets into the notes application may connect an agent "
            "(custom/README.md, AI agents sign in with Authentik)"
        )
    return AgentSignInSettings(
        issuer=(env.get("MCP_OAUTH_ISSUER") or "").strip(),
        client_id=(env.get("MCP_OAUTH_CLIENT_ID") or "").strip(),
        client_secret=(env.get("MCP_OAUTH_CLIENT_SECRET") or "").strip(),
        provider_name=(env.get("OIDC_PROVIDER_NAME") or "authentik").strip(),
    )
