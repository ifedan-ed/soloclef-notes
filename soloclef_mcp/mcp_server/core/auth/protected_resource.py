"""This server as an OAuth protected resource (RFC 9728, MCP authorization spec).

claude.ai and ChatGPT find where to sign in from the document built here: a
401 names it in ``WWW-Authenticate: Bearer resource_metadata="..."``, and it
lists Authentik as the one authorization server. Claude uses only the first
entry, so there is exactly one.
"""

from __future__ import annotations

from dataclasses import dataclass
from urllib.parse import urlsplit, urlunsplit

WELL_KNOWN = "/.well-known/oauth-protected-resource"


def _quoted(value: str) -> str:
    return '"' + value.replace("\\", "\\\\").replace('"', '\\"') + '"'


@dataclass(frozen=True)
class ProtectedResource:
    # Exactly the URL people paste into their client (e.g.
    # https://notes.example.com/mcp): clients compare it with what they asked.
    resource: str
    # The Authentik issuer, character for character as its discovery
    # document prints it (trailing slash included), or discovery fails.
    authorization_server: str
    scopes: tuple[str, ...] = ()
    name: str = "Soloclef Notes"

    @property
    def metadata_paths(self) -> tuple[str, ...]:
        """Where the document is served: RFC 9728's path for this resource
        (``/.well-known/oauth-protected-resource/mcp``) and the bare one some
        clients probe first."""
        path = urlsplit(self.resource).path.rstrip("/")
        return (f"{WELL_KNOWN}{path}", WELL_KNOWN) if path else (WELL_KNOWN,)

    @property
    def metadata_url(self) -> str:
        parts = urlsplit(self.resource)
        return urlunsplit((parts.scheme, parts.netloc, self.metadata_paths[0], "", ""))

    def metadata(self) -> dict:
        return {
            "resource": self.resource,
            "authorization_servers": [self.authorization_server],
            "scopes_supported": list(self.scopes),
            "bearer_methods_supported": ["header"],
            "resource_name": self.name,
        }

    def challenge(
        self, error: str | None = None, description: str | None = None
    ) -> str:
        """The ``WWW-Authenticate`` value for a 401 (RFC 6750 section 3)."""
        params: list[str] = []
        if error:
            params.append(f"error={_quoted(error)}")
        if description:
            params.append(f"error_description={_quoted(description)}")
        params.append(f"resource_metadata={_quoted(self.metadata_url)}")
        if self.scopes:
            params.append(f"scope={_quoted(' '.join(self.scopes))}")
        return "Bearer " + ", ".join(params)
