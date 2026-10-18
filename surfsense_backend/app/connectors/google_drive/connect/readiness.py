"""Whether this server can connect Google Drive, and what the admin still has to set.

Only setting names are ever reported, never values.
"""

from __future__ import annotations

import re

from app.config import config

_PROJECT_NUMBER = re.compile(r"^(\d+)-")
_WEB_CLIENT_SUFFIX = ".apps.googleusercontent.com"


def missing_settings() -> list[str]:
    """Unset settings, and a client ID that is not a Google OAuth client's."""
    missing = []
    client_id = config.GOOGLE_OAUTH_CLIENT_ID or ""
    if not client_id.endswith(_WEB_CLIENT_SUFFIX):
        missing.append("GOOGLE_OAUTH_CLIENT_ID")
    if not config.GOOGLE_OAUTH_CLIENT_SECRET:
        missing.append("GOOGLE_OAUTH_CLIENT_SECRET")
    if not config.GOOGLE_DRIVE_REDIRECT_URI:
        missing.append("GOOGLE_DRIVE_REDIRECT_URI")
    if not config.GOOGLE_PICKER_API_KEY:
        missing.append("GOOGLE_PICKER_API_KEY")
    if picker_app_id() is None:
        missing.append("GOOGLE_PICKER_APP_ID")
    return missing


def picker_app_id() -> str | None:
    """The Cloud project number the Picker grants picked files to.

    It is the leading number of a web client ID, so it needs setting only
    when the client ID does not start with it.
    """
    explicit = (config.GOOGLE_PICKER_APP_ID or "").strip()
    if explicit:
        return explicit
    match = _PROJECT_NUMBER.match(config.GOOGLE_OAUTH_CLIENT_ID or "")
    return match.group(1) if match else None
