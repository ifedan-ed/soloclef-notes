"""The product name and look users see: one place, mirrored from the web.

The canonical copy is ``surfsense_web/lib/brand/brand.json``; a unit test
keeps these defaults equal to it. ``BRAND_NAME`` may be overridden from the
environment (read once, at import).

The product's name, colours and logo belong to the product; the site's
address and contact belong to whoever runs the server, so neither has a
default here. ``BRAND_SITE_URL`` is this server's own public address, from
its configuration (``site_url_from_env``), and empty when it has none.

Internal identifiers (package names, env vars, loggers, cookies, headers) keep
the upstream "surfsense" spelling on purpose, so upstream merges stay small.
"""

from __future__ import annotations

import os
from collections.abc import Mapping

DEFAULT_BRAND_NAME = "Soloclef Notes"
# No address of its own: a server is wherever its operator puts it.
DEFAULT_SITE_URL = ""
# Where the public address comes from, first one set wins. BRAND_SITE_URL is
# only for a site that lives somewhere other than the app; the other two are
# the addresses every deployment already sets (docker/.env, docker-compose.yml).
# It is the address of links people read (docs, contact), not the one phones
# are set to: a push names SURFSENSE_PUBLIC_URL alone (app/push/server_origin.py).
SITE_URL_ENV = ("BRAND_SITE_URL", "NEXT_FRONTEND_URL", "SURFSENSE_PUBLIC_URL")


def site_url_from_env(env: Mapping[str, str]) -> str:
    """This server's public address without a trailing slash, or ``""``."""
    for name in SITE_URL_ENV:
        value = (env.get(name) or "").strip().rstrip("/")
        if value:
            return value
    return DEFAULT_SITE_URL


BRAND_NAME: str = os.getenv("BRAND_NAME", "").strip() or DEFAULT_BRAND_NAME
BRAND_SHORT_NAME = "Notes"
BRAND_TAGLINE = "Notebooks that answer from your sources."
BRAND_DESCRIPTION = (
    "Upload your documents and research, ask questions and get answers with "
    "citations, and turn your sources into reports, mind maps, quizzes, "
    "slides, audio and video overviews."
)
BRAND_SITE_URL: str = site_url_from_env(os.environ)
# Site-relative ("/docs") when the server has no address: still right in the
# web app, which is where chat answers and error links are opened.
BRAND_DOCS_URL = f"{BRAND_SITE_URL}/docs"
BRAND_CONTACT_URL = f"{BRAND_SITE_URL}/contact"
# No default: the address people write to is the admin-edited support email
# (app.support_contact), else SUPPORT_EMAIL in docker/.env.
BRAND_CONTACT_EMAIL = ""

BRAND_COLORS: dict[str, str] = {
    "ink": "#1f2937",
    "accent": "#2f6fed",
    "accentOnDark": "#6b9bff",
    "background": "#ffffff",
    "backgroundDark": "#0f141c",
}

BRAND_LOGO: dict[str, str] = {
    "mark": "/brand/mark.svg",
    "markMono": "/brand/mark-mono.svg",
    "wordmark": "/brand/wordmark.svg",
    "icon192": "/brand/icon-192.png",
    "icon512": "/brand/icon-512.png",
    "maskable512": "/brand/maskable-512.png",
    "og": "/og-image.png",
}


def apply_brand(text: str) -> str:
    """Fill ``{{BRAND_NAME}}`` and ``{{BRAND_DOCS_URL}}`` in a prompt fragment."""
    if "{{BRAND_" not in text:
        return text
    return text.replace("{{BRAND_NAME}}", BRAND_NAME).replace(
        "{{BRAND_DOCS_URL}}", BRAND_DOCS_URL
    )
