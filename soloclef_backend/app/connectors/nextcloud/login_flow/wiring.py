"""Production wiring of the connect routes: Redis for pending logins and limits, real HTTP."""

from __future__ import annotations

from collections.abc import Callable
from functools import lru_cache

import redis.asyncio as aioredis

from app.config import config

from ..client import NextcloudAccount, NextcloudClient
from .attempt_limits import AttemptLimits
from .login_api import NextcloudLoginApi
from .pending_logins import PendingLogins

LoginApiFactory = Callable[[str], NextcloudLoginApi]
WebdavClientFactory = Callable[[NextcloudAccount], NextcloudClient]


@lru_cache(maxsize=1)
def _redis() -> aioredis.Redis:
    return aioredis.from_url(config.REDIS_APP_URL, decode_responses=True)


def get_pending_logins() -> PendingLogins:
    return PendingLogins(_redis())


def get_attempt_limits() -> AttemptLimits:
    return AttemptLimits(_redis())


def get_login_api_factory() -> LoginApiFactory:
    return NextcloudLoginApi


def get_webdav_client_factory() -> WebdavClientFactory:
    return NextcloudClient
