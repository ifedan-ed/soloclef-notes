"""E2E Celery worker entrypoint.

Same LLM/embedding patches as run_backend.py,
applied before importing the production celery_app. Celery workers
run in a separate Python interpreter, so the patches must be applied
here too — they would NOT carry over from the FastAPI process.

Production is unaffected: celery_worker.py at the repo root is the
production entrypoint and never imports this file.

Usage:
    cd surfsense_backend
    uv run python tests/e2e/run_celery.py
"""

from __future__ import annotations

import logging
import os
import sys

_THIS_DIR = os.path.dirname(os.path.abspath(__file__))
_BACKEND_ROOT = os.path.abspath(os.path.join(_THIS_DIR, "..", ".."))
if _BACKEND_ROOT not in sys.path:
    sys.path.insert(0, _BACKEND_ROOT)


logger = logging.getLogger("surfsense.e2e.celery")

# Patches started during bootstrap are kept alive for the lifetime of the
# process. We never call .stop() on them.
_active_patches: list = []


def _load_dotenv_and_set_env_defaults() -> None:
    """Load .env and set every env var the production config reads on import.

    MUST run before _import_celery_app(), since app.config consumes
    these values at import time.
    """
    from dotenv import load_dotenv

    os.environ.setdefault(
        "DATABASE_URL",
        "postgresql+asyncpg://postgres:postgres@localhost:5432/surfsense",
    )
    os.environ.setdefault("CELERY_BROKER_URL", "redis://localhost:6379/0")
    os.environ.setdefault("CELERY_RESULT_BACKEND", "redis://localhost:6379/0")
    os.environ.setdefault("REDIS_APP_URL", "redis://localhost:6379/0")
    os.environ.setdefault("CELERY_TASK_DEFAULT_QUEUE", "surfsense")
    os.environ.setdefault("SECRET_KEY", "local-e2e-secret-not-for-production")
    os.environ.setdefault("AUTH_TYPE", "LOCAL")
    os.environ.setdefault("REGISTRATION_ENABLED", "TRUE")
    os.environ.setdefault("ETL_DEFAULT_PARSER", "LOCAL")
    os.environ.setdefault("EMBEDDING_MODEL", "sentence-transformers/all-MiniLM-L6-v2")
    os.environ.setdefault("NEXT_FRONTEND_URL", "http://localhost:3000")

    # Sentinel keys — fakes never read them; turns leaked real calls into 401s.
    os.environ.setdefault("OPENAI_API_KEY", "local-deny-real-call-sentinel")
    os.environ.setdefault("ANTHROPIC_API_KEY", "local-deny-real-call-sentinel")
    os.environ.setdefault("LITELLM_API_KEY", "local-deny-real-call-sentinel")

    # Google (OAuth, Drive) is the stand-in server tests/e2e/fakes/google_server.py,
    # reached through the GOOGLE_* endpoint overrides the e2e compose file sets.
    os.environ.setdefault(
        "GOOGLE_DRIVE_REDIRECT_URI",
        "http://localhost:8000/api/v1/auth/google/drive/connector/callback",
    )

    # Load .env last so the E2E defaults above win over a developer's .env
    # (e.g. AUTH_TYPE=GOOGLE), while an explicitly exported shell var still
    # beats both: setdefault respects it and load_dotenv() never overrides.
    load_dotenv()


def _install_synthetic_global_llm_config() -> None:
    """Materialise a fake ``app/config/global_llm_config.yaml`` for E2E.

    The real file is gitignored (production operators ship their own with
    real API keys), so a fresh CI checkout has no YAML at the path
    ``app.config.load_global_llm_configs()`` reads. With an empty
    ``GLOBAL_LLM_CONFIGS`` list, the worker's view of the config diverges
    from the API container.

    We copy the synthetic fixture from ``tests/e2e/fixtures/`` into the
    production-expected location BEFORE _import_celery_app() so
    ``app.config`` picks it up on import. Install-only-if-missing so a
    developer's local config (with real API keys) is preserved.

    MUST run before _import_celery_app().
    """
    import shutil

    src = os.path.join(_THIS_DIR, "fixtures", "global_llm_config.yaml")
    dst = os.path.join(_BACKEND_ROOT, "app", "config", "global_llm_config.yaml")

    if not os.path.exists(src):
        raise RuntimeError(
            f"E2E synthetic global LLM config fixture missing at {src!r}. "
            f"Restore tests/e2e/fixtures/global_llm_config.yaml from VCS."
        )

    if os.path.exists(dst):
        logger.info(
            "[e2e-global-llm-config] %s already exists; leaving it alone "
            "(local dev config preserved)",
            dst,
        )
        return

    os.makedirs(os.path.dirname(dst), exist_ok=True)
    shutil.copyfile(src, dst)
    logger.info("[e2e-global-llm-config] installed %s -> %s", src, dst)


def _import_celery_app():
    """Import and return the production Celery app.

    Every module under ``app.*`` (including all task modules) loads here,
    creating their bindings. The LLM/embedding factories captured at this
    point will be replaced by patches in _patch_llm_bindings() below.
    """
    from app.celery_app import celery_app

    return celery_app


def _patch_llm_bindings() -> None:
    """Replace LLM factories at every known binding site in worker tasks."""
    from unittest.mock import patch

    from tests.e2e.fakes.chat_llm import (
        fake_build_profiled_llm,
        fake_create_chat_litellm_from_agent_config,
        fake_create_chat_litellm_from_config,
    )
    from tests.e2e.fakes.llm import fake_get_agent_llm

    targets = [
        "app.services.llm_service.get_agent_llm",
        "app.tasks.connector_indexers.google_drive_indexer.get_agent_llm",
        "app.tasks.connector_indexers.local_folder_indexer.get_agent_llm",
        "app.tasks.document_processors._save.get_agent_llm",
        "app.tasks.document_processors.markdown_processor.get_agent_llm",
    ]
    for target in targets:
        try:
            p = patch(target, fake_get_agent_llm)
            p.start()
            _active_patches.append(p)
            logger.info("[fake-llm] patched %s in celery worker", target)
        except (ModuleNotFoundError, AttributeError) as exc:
            logger.warning(
                "[fake-llm] could not patch %s in celery worker: %s.",
                target,
                exc,
            )

    chat_targets = [
        (
            "app.agents.chat.runtime.llm_config.create_chat_litellm_from_agent_config",
            fake_create_chat_litellm_from_agent_config,
        ),
        (
            "app.agents.chat.runtime.llm_config.create_chat_litellm_from_config",
            fake_create_chat_litellm_from_config,
        ),
        (
            "app.tasks.chat.streaming.flows.shared.llm_bundle.create_chat_litellm_from_agent_config",
            fake_create_chat_litellm_from_agent_config,
        ),
        (
            "app.tasks.chat.streaming.flows.shared.llm_bundle.create_chat_litellm_from_config",
            fake_create_chat_litellm_from_config,
        ),
        # The chat stream now builds its model in _build_profiled_llm rather
        # than through the create_chat_litellm_* factories above.
        (
            "app.tasks.chat.streaming.flows.shared.llm_bundle._build_profiled_llm",
            fake_build_profiled_llm,
        ),
    ]
    for target, replacement in chat_targets:
        try:
            p = patch(target, replacement)
            p.start()
            _active_patches.append(p)
            logger.info("[fake-chat-llm] patched %s in celery worker", target)
        except (ModuleNotFoundError, AttributeError) as exc:
            logger.warning(
                "[fake-chat-llm] could not patch %s in celery worker: %s.",
                target,
                exc,
            )


def _install_runtime_fakes() -> None:
    """Run each fake's install() against the active patch stack."""
    from tests.e2e.fakes import (
        browserbase as _fake_browserbase,
        embeddings as _fake_embeddings,
        mcp_runtime as _fake_mcp_runtime,
        pymupdf4llm_parser as _fake_pymupdf4llm_parser,
        quick_llm as _fake_quick_llm,
    )

    _fake_embeddings.install(_active_patches)
    _fake_quick_llm.install(_active_patches)
    _fake_pymupdf4llm_parser.install(_active_patches)
    _fake_mcp_runtime.install(_active_patches)
    _fake_browserbase.install(_active_patches)


def _bootstrap():
    """Run the full E2E bootstrap and return the production Celery app.

    Ordering is load-bearing:
      1) Load .env + set env defaults (app.config reads env on import).
      2) Configure logging.
      3) Materialise the synthetic global_llm_config.yaml so the worker's
         view of GLOBAL_LLM_CONFIGS matches the API container.
      4) Import production celery_app (which reads the env defaults + YAML).
      5) Patch LLM / embedding bindings at every consumer site.
      6) Install runtime fakes for connectors and chat backends.
    """
    _load_dotenv_and_set_env_defaults()

    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s - %(name)s - %(levelname)s - %(message)s",
        datefmt="%Y-%m-%d %H:%M:%S",
    )
    logger.warning("*** SURFSENSE E2E CELERY WORKER — fake LLM + embeddings ***")

    _install_synthetic_global_llm_config()
    celery_app = _import_celery_app()
    _patch_llm_bindings()
    _install_runtime_fakes()
    return celery_app


celery_app = _bootstrap()


def _main() -> None:
    queue_name = os.getenv("CELERY_TASK_DEFAULT_QUEUE", "surfsense")
    queues = f"{queue_name},{queue_name}.connectors"

    # macOS forks-after-MPS-init crash prefork workers; threads avoid it.
    default_pool = "threads" if sys.platform == "darwin" else "prefork"
    pool = os.getenv("CELERY_POOL", default_pool)
    concurrency = os.getenv("CELERY_CONCURRENCY", "2")

    celery_app.worker_main(
        argv=[
            "worker",
            "--loglevel=info",
            f"--queues={queues}",
            f"--pool={pool}",
            f"--concurrency={concurrency}",
            "--without-gossip",
            "--without-mingle",
        ]
    )


if __name__ == "__main__":
    _main()
