"""Notifications HTTP API: the inbox's reads and mark-read, and deleting from it."""

from __future__ import annotations

from fastapi import APIRouter

from app.notifications.api.api import router as inbox_router
from app.notifications.api.deletion import router as deletion_router

router = APIRouter()
router.include_router(inbox_router)
router.include_router(deletion_router)

__all__ = ["router"]
