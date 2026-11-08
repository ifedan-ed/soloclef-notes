"""Soloclef filesystem middleware (multi-agent flavour)."""

from __future__ import annotations

from .index import build_filesystem_mw
from .middleware import SoloclefFilesystemMiddleware

__all__ = [
    "SoloclefFilesystemMiddleware",
    "build_filesystem_mw",
]
