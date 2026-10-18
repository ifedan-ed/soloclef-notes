"""Configuration for the file-storage module, sourced from the central Config."""

from __future__ import annotations

from dataclasses import dataclass

LOCAL_BACKEND = "local"


@dataclass(frozen=True)
class StorageSettings:
    """Resolved storage configuration for the current process."""

    backend: str
    local_root: str


def load_storage_settings() -> StorageSettings:
    """Resolve storage settings from the central ``Config`` singleton.

    ``local`` is the only backend.
    """
    from app.config import config

    return StorageSettings(
        backend=config.FILE_STORAGE_BACKEND,
        local_root=config.FILE_STORAGE_LOCAL_PATH,
    )
