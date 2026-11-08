"""Login flows the backend is still polling, kept in Redis until they expire.

Each is bound to the person and workspace that started it; the browser only
holds a random id. The poll token is stored encrypted and never leaves the
backend, so nobody else's id (or a leaked one) can finish a connection.
Nextcloud forgets a login flow after 20 minutes; so does this.
"""

from __future__ import annotations

import json
import secrets
from dataclasses import asdict, dataclass
from typing import Protocol
from uuid import UUID

from app.config import config
from app.utils.oauth_security import TokenEncryption

LIFETIME_SECONDS = 20 * 60
_KEY = "soloclef:nextcloud:login-flow:{}"


@dataclass(frozen=True)
class PendingLogin:
    user_id: str
    workspace_id: int
    poll_endpoint: str
    poll_token: str
    login_url: str

    def belongs_to(self, user_id: UUID, workspace_id: int) -> bool:
        return self.user_id == str(user_id) and self.workspace_id == workspace_id


class KeyValueStore(Protocol):
    async def set(self, key: str, value: str, ex: int) -> object: ...
    async def get(self, key: str) -> str | None: ...
    async def delete(self, key: str) -> object: ...


def new_flow_id() -> str:
    return secrets.token_urlsafe(24)


class PendingLogins:
    def __init__(self, store: KeyValueStore, secret_key: str | None = None) -> None:
        self._store = store
        self._cipher = TokenEncryption(secret_key or config.SECRET_KEY)

    async def add(self, flow_id: str, login: PendingLogin) -> None:
        record = asdict(login)
        record["poll_token"] = self._cipher.encrypt_token(login.poll_token)
        await self._store.set(
            _KEY.format(flow_id), json.dumps(record), ex=LIFETIME_SECONDS
        )

    async def get(self, flow_id: str) -> PendingLogin | None:
        raw = await self._store.get(_KEY.format(flow_id))
        if not raw:
            return None
        record = json.loads(raw)
        record["poll_token"] = self._cipher.decrypt_token(record["poll_token"])
        return PendingLogin(**record)

    async def remove(self, flow_id: str) -> None:
        await self._store.delete(_KEY.format(flow_id))
