"""The admin-editable list of MCP server hosts, and who the administrators are.

The list applies to site servers only (those an administrator adds, the
services the site provides on its own keys); a workspace member's own server
is not held to it (``server_origin``). Stored as the plain
``mcp_allowed_hosts`` row of ``billing_settings`` (the site's admin key/value
table; the billing settings page ignores keys it does not know). With no row
the defaults below apply: the three remote servers the site was set up with.
"""

from __future__ import annotations

import logging
import re
import time
from datetime import UTC, datetime
from uuid import UUID

from sqlalchemy.ext.asyncio import AsyncSession

from app.billing.ledger import is_config_admin
from app.billing.persistence.models import BillingSetting
from app.db import User

from .url_guard import normalize_host

logger = logging.getLogger(__name__)

SETTING_KEY = "mcp_allowed_hosts"
DEFAULT_ALLOWED_HOSTS: tuple[str, ...] = (
    "mcp.exa.ai",
    "mcp.tavily.com",
    "mcp.browserbase.com",
)
_CACHE_TTL_SECONDS = 60.0
_HOSTNAME = re.compile(
    r"^(?=.{1,253}$)(?!-)[a-z0-9-]{1,63}(?<!-)(\.(?!-)[a-z0-9-]{1,63}(?<!-))+$"
)

_cache: tuple[float, frozenset[str]] | None = None


def is_mcp_admin(user: User | None) -> bool:
    """Site administrators: they edit the allowlist and any MCP server, and the
    servers they add are site servers, held to the allowlist."""
    return is_config_admin(user)


def _from_value(value: object) -> frozenset[str]:
    if not isinstance(value, list):
        return frozenset(DEFAULT_ALLOWED_HOSTS)
    return frozenset(normalize_host(h) for h in value if isinstance(h, str) and h)


def clean_hosts(hosts: list[str]) -> list[str]:
    """Validated, lower-cased, de-duplicated host names; raises ``ValueError``."""
    out: list[str] = []
    for raw in hosts:
        host = normalize_host(str(raw))
        if not _HOSTNAME.match(host):
            raise ValueError(f"Not a host name: {raw!r}")
        if host not in out:
            out.append(host)
    return sorted(out)


async def load_allowed_hosts(session: AsyncSession) -> frozenset[str]:
    """Read the list from the database and refresh the process cache."""
    global _cache
    row = await session.get(BillingSetting, SETTING_KEY)
    hosts = _from_value(row.value if row is not None else None)
    _cache = (time.monotonic(), hosts)
    return hosts


async def current_allowed_hosts() -> frozenset[str]:
    """The list for call-time checks, cached for a minute."""
    cached = _cache
    if cached is not None and time.monotonic() - cached[0] < _CACHE_TTL_SECONDS:
        return cached[1]
    from app.db import async_session_maker

    try:
        async with async_session_maker() as session:
            return await load_allowed_hosts(session)
    except Exception:
        logger.warning("Could not read the MCP host allowlist", exc_info=True)
        return cached[1] if cached is not None else frozenset(DEFAULT_ALLOWED_HOSTS)


async def save_allowed_hosts(
    session: AsyncSession, hosts: list[str], *, actor: UUID | None
) -> list[str]:
    global _cache
    cleaned = clean_hosts(hosts)
    row = await session.get(BillingSetting, SETTING_KEY)
    if row is None:
        row = BillingSetting(key=SETTING_KEY, secret=False)
        session.add(row)
    row.value = cleaned
    row.updated_at = datetime.now(UTC)
    row.updated_by = actor
    await session.flush()
    _cache = (time.monotonic(), frozenset(cleaned))
    return cleaned


def reset_cache() -> None:
    global _cache
    _cache = None
