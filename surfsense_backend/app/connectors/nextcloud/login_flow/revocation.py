"""Revoking a stored Nextcloud app password, so a removed connection is dead in Nextcloud too."""

from __future__ import annotations

import logging
from typing import Any

from ..connector_credentials import InvalidNextcloudSettingsError, account_from_config
from .login_api import NextcloudLoginApi

logger = logging.getLogger(__name__)


async def revoke_stored_password(
    connector_config: dict[str, Any] | None,
    *,
    api_for=None,
) -> bool:
    """Best effort: False when there was nothing to revoke or Nextcloud did not confirm.

    Only the configured server is contacted; a row naming another one is
    simply dropped.
    """
    try:
        account = account_from_config(connector_config)
    except (InvalidNextcloudSettingsError, ValueError):
        return False
    api = (api_for or NextcloudLoginApi)(account.base_url)
    return await api.revoke(account.auth_name, account.app_password)
