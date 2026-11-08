"""Test-only: create an account the way an administrator does in production.

``POST /__e2e__/accounts {"email"}`` (mint secret required) goes through
``create_verified_account`` (what ``scripts/create_account.py`` runs), so a
spec gets a real, verified second account without the sign-up route and its
3-a-minute-per-IP limit. Mounted by ``run_backend.py``; ``tests/`` never ships.
"""

from __future__ import annotations

import contextlib

from fastapi import APIRouter, FastAPI, Header, HTTPException
from pydantic import BaseModel

from app.account_provisioning.create import AccountExistsError, create_verified_account
from tests.e2e.auth_mint import _expected_secret

router = APIRouter(prefix="/__e2e__", tags=["__e2e__"])


class NewAccount(BaseModel):
    email: str


@router.post("/accounts", status_code=201)
async def create_account(
    body: NewAccount, x_e2e_mint_secret: str = Header(..., alias="X-E2E-Mint-Secret")
) -> dict:
    if x_e2e_mint_secret != _expected_secret():
        raise HTTPException(status_code=403, detail="invalid e2e mint secret")
    with contextlib.suppress(AccountExistsError):
        await create_verified_account(body.email)
    return {"email": body.email.strip().lower()}


def install(app: FastAPI) -> None:
    app.include_router(router)
