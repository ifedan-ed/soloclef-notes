"""Billing HTTP routes."""

from fastapi import APIRouter

from .admin import router as admin_router
from .user import router as user_router
from .webhooks import router as webhooks_router

router = APIRouter()
router.include_router(user_router)
router.include_router(webhooks_router)
router.include_router(admin_router)
