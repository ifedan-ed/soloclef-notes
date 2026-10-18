"""Which server a push comes from, so a phone that has moved on can ignore it.

A phone can be set to another Soloclef server (Change server) while this one
still holds its Expo token: until the token is dropped here (the app's
sign-out, the phone registering with another account, Expo's receipts), this
server's pushes keep reaching it. So every push names its sender as
``data["server"]``, and the app ignores a push, received or tapped, whose
``server`` is not the origin of the server it is set to; a push without the
key (an older server's, or this one when it cannot name itself) it handles as
before. custom/docs/push-notifications.md, "What a push is, and is not".

**Which address.** The one phones are set to: ``SURFSENSE_PUBLIC_URL``, and
nothing else. The app keeps one address per server and sends everything to
it (soloclef_mobile src/lib/api/server.ts): the ``/api/v1`` calls, the
password sign-in at ``/auth/desktop/login``, the single sign-on's start at
``/api/v1/auth/oidc/app/start`` (whose provider callback comes back through
``BACKEND_URL``, itself ``SURFSENSE_PUBLIC_URL`` unless set) and the website
pages it opens. Only the single public address, where the bundled proxy
(docker/proxy/Caddyfile) serves the website, ``/api/v1``, ``/auth`` and
``/users`` together, answers all of them, and it is the address
self-hosting.mdx ("The phone app") tells people to type. ``BRAND_SITE_URL``,
``app.brand``'s first choice, is a public site somewhere other than the app,
which no phone is set to; ``NEXT_FRONTEND_URL`` and ``BACKEND_URL`` are the
address of one part only. A wrong name is worse than none: a push naming
another origin is dropped on every phone, a push naming none is shown.

**Which form.** What the app makes of the same address (its
``features/server/address.ts`` when someone types it, ``lib/push/deepLinks.ts``
``originOf`` when it compares): ``scheme://host[:port]`` as a browser's
``URL.origin`` gives it, the host in ASCII (an international name in its
``xn--`` form, converted as browsers convert it, UTS #46), lower case, no
trailing dot, no default port, no path. The app takes a host of letters,
digits and hyphens (each label 1-63 characters, no hyphen at either end, 253
in all) or a dotted IPv4 address, and nothing else: no underscore, no IPv6
address, no port 0. Any other address gives ``""`` (no key), since the app
could never match it and would drop every push.

**https only.** The app is never set to a plain-http server (it refuses
``http://`` addresses), so an http ``SURFSENSE_PUBLIC_URL`` names no server
either: named, it would make the app drop every push. ``push_origin_warning``
says so in the startup log while push is on (``app.py``'s lifespan, and the
Celery worker, which sends the pushes, when it is ready).
"""

from __future__ import annotations

import logging
import re
from urllib.parse import urlsplit

# A dependency of httpx. It converts as browsers do (UTS #46); the standard
# library's "idna" codec is IDNA 2003, which turns "faß.de" into "fass.de".
import idna

from app.config import config

logger = logging.getLogger(__name__)

# The schemes a server is reached on, and the port each implies (a port equal
# to it is left out of the origin, as URL.origin leaves it out).
_DEFAULT_PORTS = {"http": 80, "https": 443}

# The app's address rules (soloclef_mobile src/features/server/address.ts,
# LABEL, IPV4 and validHost): what a host must be for a phone to be set to it.
_HOST_MAX = 253
_LABEL = re.compile(r"^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$")
_IPV4 = re.compile(r"^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$")


def _ascii_host(host: str) -> str:
    """The host as the app keeps it, or ``""`` when the app would refuse it."""
    host = host.lower().removesuffix(".")
    if not host.isascii():
        # As a browser's URL parser does, non-transitional: "faß.de" is
        # "xn--fa-hia.de". A name it cannot convert (an emoji) names nothing.
        try:
            host = idna.encode(host, uts46=True).decode("ascii")
        except idna.IDNAError:
            return ""
    if not host or len(host) > _HOST_MAX:
        return ""
    ip = _IPV4.match(host)
    if ip:
        return host if all(int(part) <= 255 for part in ip.groups()) else ""
    if host.replace(".", "").isdigit():  # "1234", "1.2.3": not an address
        return ""
    return host if all(_LABEL.match(label) for label in host.split(".")) else ""


def origin_of(address: str) -> str:
    """``"https://Bücher.Example:443/x/"`` -> ``"https://xn--bcher-kva.example"``; ``""`` for anything the app could not match."""
    try:
        parts = urlsplit((address or "").strip())
        port = parts.port
    except ValueError:  # a port that is not a number, or above 65535
        return ""
    scheme = parts.scheme.lower()
    if scheme not in _DEFAULT_PORTS or not parts.hostname or port == 0:
        return ""
    if ":" in parts.hostname:  # IPv6: the app takes no bracketed address
        return ""
    host = _ascii_host(parts.hostname)
    if not host:
        return ""
    if port is None or port == _DEFAULT_PORTS[scheme]:
        return f"{scheme}://{host}"
    return f"{scheme}://{host}:{port}"


def _push_address() -> str:
    """``SURFSENSE_PUBLIC_URL``, the address phones are set to (the module's docstring says why)."""
    return (config.SURFSENSE_PUBLIC_URL or "").strip()


def this_server() -> str:
    """This server's origin for ``data["server"]``; ``""`` (no key) when phones could not match it."""
    origin = origin_of(_push_address())
    return origin if origin.startswith("https://") else ""


def push_origin_warning() -> str:
    """Why phones cannot tell this server's pushes from another's, or ``""``.

    Only while ``PUSH_ENABLED`` is on: with it off nothing is sent. It names
    the origin at most, never the address as written, which could carry a
    name and password.
    """
    if not config.PUSH_ENABLED or this_server():
        return ""
    address = _push_address()
    origin = origin_of(address)
    if not address:
        problem = "SURFSENSE_PUBLIC_URL is not set"
    elif origin.startswith("http://"):
        problem = (
            f"SURFSENSE_PUBLIC_URL is {origin}, not https. The phone app is only "
            "ever set to https addresses and would drop every push naming this one"
        )
    else:
        problem = (
            "SURFSENSE_PUBLIC_URL is not an address the phone app takes (https, "
            "a host of letters, digits and hyphens or an IPv4 address; no "
            "underscore, no IPv6)"
        )
    return (
        f"{problem}, so pushes name no server: a phone moved to another server "
        "under Change server keeps showing this server's pushes. Set "
        "SURFSENSE_PUBLIC_URL in docker/.env to the https address people type "
        "in the phone app."
    )


def warn_about_push_origin() -> None:
    """Log ``push_origin_warning`` once at startup, when there is one."""
    message = push_origin_warning()
    if message:
        logger.warning("[startup] push: %s", message)
