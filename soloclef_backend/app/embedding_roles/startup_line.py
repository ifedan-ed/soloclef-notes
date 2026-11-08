"""Say at startup which model embeds questions and which documents.

Logged once by the backend and once by the Celery worker, so a deploy can
check the models, their shared vector length and the document limits in the
log: ``[startup] embeddings: ...``.
"""

from __future__ import annotations

import logging

from app.config import config

logger = logging.getLogger(__name__)


def log_embedding_roles() -> None:
    logger.info("[startup] embeddings: %s", config.EMBEDDING_ROLES_SUMMARY)
