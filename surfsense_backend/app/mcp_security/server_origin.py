"""Who provided an MCP server, which decides where it may connect.

- ``site``: added by a site administrator (``BILLING_ADMIN_EMAILS``): a
  service the site provides on its own keys, such as Exa, Tavily and
  Browserbase. Its host must be on the admin allow-list (``allowlist.py``).
- ``member``: added by a workspace Owner or Editor with their own key, on
  their own bill. No allow-list: any host passes that is ``https`` on the
  default port, named (not an IP address), without credentials in the URL,
  and resolves only to public addresses. The connection is pinned to the
  address just checked (DNS-rebinding safe) and redirects are not followed,
  exactly as for site servers (``url_guard.py``), so a member's server can
  never be used to reach Soloclef's own network.

The origin is stored in the connector's config by the server when the row is
created and never taken from a request, so nobody can promote their own row.
Rows from before the split carry no marker and count as ``site``: the three
shared servers keep exactly the rules they had.
"""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any, Literal
from urllib.parse import urlsplit

from sqlalchemy.ext.asyncio import AsyncSession

from app.db import User

from .allowlist import current_allowed_hosts, is_mcp_admin, load_allowed_hosts
from .url_guard import MCPDestinationRefusedError, normalize_host

Origin = Literal["site", "member"]
SITE: Origin = "site"
MEMBER: Origin = "member"
ORIGIN_KEY = "server_origin"


def origin_for_new_server(user: User | None) -> Origin:
    return SITE if is_mcp_admin(user) else MEMBER


def origin_of(config: Mapping[str, Any] | None) -> Origin:
    """The stored origin; anything unmarked or unknown is a site server."""
    value = (config or {}).get(ORIGIN_KEY)
    return MEMBER if value == MEMBER else SITE


# Names that only mean something inside a network. A member's server must be
# named by a public domain (a dot, and none of these endings), so a Docker
# service name such as ``litellm`` or ``backend`` is refused before any DNS
# lookup; the public-address check after resolution still applies to all.
_PRIVATE_NAME_ENDINGS = (".local", ".localhost", ".internal", ".lan", ".home.arpa")


def _host_of(url: Any) -> str | None:
    if not isinstance(url, str):
        return None
    try:
        host = urlsplit(url.strip()).hostname
    except ValueError:
        return None
    return normalize_host(host) if host else None


def _is_public_name(host: str) -> bool:
    return "." in host and not host.endswith(_PRIVATE_NAME_ENDINGS)


def own_host(url: str) -> frozenset[str]:
    """A member server may reach its own host and nothing else."""
    host = _host_of(url)
    return frozenset({host}) if host and _is_public_name(host) else frozenset()


async def reachable_hosts(origin: Origin, url: str) -> frozenset[str]:
    """Hosts a connection for this server may reach, at call time (the
    allow-list from its one-minute cache, so an admin's edit lands quickly)."""
    if origin == MEMBER:
        return own_host(url)
    return await current_allowed_hosts()


async def reachable_hosts_for_save(
    session: AsyncSession, origin: Origin, url: str
) -> frozenset[str]:
    """The same, read fresh from the database, for saving and testing."""
    if origin == MEMBER:
        host = _host_of(url)
        if host and not _is_public_name(host):
            raise MCPDestinationRefusedError(
                "Use the server's public address (a full domain name)"
            )
        return own_host(url)
    return await load_allowed_hosts(session)
