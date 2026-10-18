"""Test-only: make a public link expire now, so a spec can see the expired page.

``POST /__e2e__/public-links/{snapshot_id}/expire`` (mint secret required)
sets ``expires_at`` a second into the past; ``.../notebook/{link_id}/expire``
does the same for a live notebook link. Mounted by ``run_backend.py``;
``tests/`` never ships.
"""

from __future__ import annotations

from datetime import UTC, datetime, timedelta

from fastapi import APIRouter, FastAPI, Header, HTTPException

from app.db import PublicChatSnapshot, async_session_maker
from tests.e2e.auth_mint import _expected_secret

router = APIRouter(prefix="/__e2e__/public-links", tags=["__e2e__"])


@router.post("/{snapshot_id}/expire")
async def expire_public_link(
    snapshot_id: int, x_e2e_mint_secret: str = Header(..., alias="X-E2E-Mint-Secret")
) -> dict:
    if x_e2e_mint_secret != _expected_secret():
        raise HTTPException(status_code=403, detail="invalid e2e mint secret")
    async with async_session_maker() as session:
        snapshot = await session.get(PublicChatSnapshot, snapshot_id)
        if snapshot is None:
            raise HTTPException(status_code=404, detail="no such public link")
        snapshot.expires_at = datetime.now(UTC) - timedelta(seconds=1)
        await session.commit()
    return {"expired": snapshot_id}


@router.post("/notebook/{link_id}/expire")
async def expire_notebook_link(
    link_id: int, x_e2e_mint_secret: str = Header(..., alias="X-E2E-Mint-Secret")
) -> dict:
    from app.public_sharing.notebook_links.models import NotebookPublicLink

    if x_e2e_mint_secret != _expected_secret():
        raise HTTPException(status_code=403, detail="invalid e2e mint secret")
    async with async_session_maker() as session:
        link = await session.get(NotebookPublicLink, link_id)
        if link is None:
            raise HTTPException(status_code=404, detail="no such notebook link")
        link.expires_at = datetime.now(UTC) - timedelta(seconds=1)
        await session.commit()
    return {"expired": link_id}


def install(app: FastAPI) -> None:
    app.include_router(router)
