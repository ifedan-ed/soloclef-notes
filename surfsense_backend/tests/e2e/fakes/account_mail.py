"""Password-reset mail and Turnstile for the E2E stack, which has no route out.

- Mail: the reset and invitation emails go to an in-memory outbox instead of SMTP;
  ``GET /__e2e__/mail/last?to=`` (mint secret required) returns the newest
  message to an address, so a spec can follow the real link.
- Turnstile: Cloudflare's siteverify is unreachable, so the verifier answers
  the way Cloudflare answers its documented test secrets: ``1x...`` passes any
  token, ``2x...`` fails every token. The route dependency, the header and the
  refusals are the production code's own.

Mounted only by ``tests/e2e/run_backend.py``; ``tests/`` never ships.
"""

from __future__ import annotations

import logging
from typing import Any

from fastapi import APIRouter, FastAPI, Header, HTTPException

from tests.e2e.auth_mint import _expected_secret

_logger = logging.getLogger("surfsense.e2e.account_mail")

OUTBOX: list[Any] = []


class _OutboxMailer:
    async def send(self, message) -> None:
        OUTBOX.append(message)


_mailer = _OutboxMailer()


async def _fake_verify(token: str, remote_ip: str | None = None, **_: Any):
    from app.auth.turnstile import TurnstileResult
    from app.config import config

    secret = config.TURNSTILE_SECRET_KEY
    if secret.startswith("2x"):
        return TurnstileResult(success=False, error_codes=["invalid-input-response"])
    return TurnstileResult(success=bool(token))


def install(patches: list[Any]) -> None:
    from unittest.mock import patch

    for target, replacement in (
        ("app.password_reset.delivery.get_mailer", lambda: _mailer),
        ("app.password_reset.delivery.is_mail_enabled", lambda: True),
        ("app.password_reset.delivery._within_address_cap", lambda email: True),
        ("app.workspace_invites.delivery.get_mailer", lambda: _mailer),
        ("app.workspace_invites.delivery.is_mail_enabled", lambda: True),
        ("app.auth.turnstile.verify_turnstile_token", _fake_verify),
    ):
        p = patch(target, replacement)
        p.start()
        patches.append(p)
    _logger.warning("[e2e] reset mail goes to the in-memory outbox; Turnstile is faked")


router = APIRouter(prefix="/__e2e__/mail", tags=["__e2e__"])


@router.get("/last")
async def last_message(
    to: str, x_e2e_mint_secret: str = Header(..., alias="X-E2E-Mint-Secret")
) -> dict:
    if x_e2e_mint_secret != _expected_secret():
        raise HTTPException(status_code=403, detail="invalid e2e mint secret")
    for message in reversed(OUTBOX):
        if message.to.lower() == to.strip().lower():
            return {
                "to": message.to,
                "sender": message.sender,
                "subject": message.subject,
                "text": message.text_body,
            }
    raise HTTPException(status_code=404, detail="no mail for that address")


def install_routes(app: FastAPI) -> None:
    app.include_router(router)
