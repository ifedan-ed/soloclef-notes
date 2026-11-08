"""Root conftest — shared fixtures available to all test modules."""

from __future__ import annotations

import os

_DEFAULT_TEST_DB = (
    "postgresql+asyncpg://postgres:postgres@localhost:5432/surfsense_test"
)
TEST_DATABASE_URL = os.environ.get("TEST_DATABASE_URL", _DEFAULT_TEST_DB)

# Force the app to use the test database regardless of any pre-existing
# DATABASE_URL in the environment (e.g. from .env or shell profile).
os.environ["DATABASE_URL"] = TEST_DATABASE_URL

# Integration tests authenticate over HTTP via email/password, so the
# password-auth routers must be mounted (they are skipped under AUTH_TYPE=GOOGLE).
# setdefault (not load_dotenv, which runs later with override=False) lets a
# developer's .env=GOOGLE be overridden here while still honouring an explicitly
# exported shell AUTH_TYPE.
os.environ.setdefault("AUTH_TYPE", "LOCAL")
os.environ.setdefault("REGISTRATION_ENABLED", "TRUE")

# app.config builds an embedding client while it is being imported, and this
# server only accepts a model behind a provider -- it runs none of its own. So
# the suite brings its own provider (tests/fake_embeddings.py: deterministic
# vectors, no model, no network) and names it here, before any import of app
# reaches app.config. That is why `uv run pytest -m unit` no longer needs an
# EMBEDDING_MODEL in front of it. An exported EMBEDDING_MODEL still wins, for a
# run deliberately pointed at a real host.
from tests import fake_embeddings  # noqa: E402

fake_embeddings.register()
os.environ.setdefault("EMBEDDING_MODEL", fake_embeddings.MODEL_NAME)

# A child interpreter inherits that EMBEDDING_MODEL but not the registration,
# so tests that start one (the import-cost probes) get it from
# tests/subprocess_site/sitecustomize.py, which Python imports as it starts up.
_BACKEND_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
os.environ["PYTHONPATH"] = os.pathsep.join(
    path
    for path in (
        os.path.join(_BACKEND_ROOT, "tests", "subprocess_site"),
        _BACKEND_ROOT,
        os.environ.get("PYTHONPATH"),
    )
    if path
)

import pytest  # noqa: E402

from app.db import DocumentType  # noqa: E402
from app.indexing_pipeline.connector_document import ConnectorDocument  # noqa: E402

# ---------------------------------------------------------------------------
# Unit test fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def sample_user_id() -> str:
    return "00000000-0000-0000-0000-000000000001"


@pytest.fixture
def sample_workspace_id() -> int:
    return 1


@pytest.fixture
def sample_connector_id() -> int:
    return 42


@pytest.fixture
def make_connector_document():
    """
    Generic factory for unit tests. Overridden in tests/integration/conftest.py
    with real DB-backed IDs for integration tests.
    """

    def _make(**overrides):
        defaults = {
            "title": "Test Document",
            "source_markdown": "## Heading\n\nSome content.",
            "unique_id": "test-id-001",
            "document_type": DocumentType.GOOGLE_DRIVE_FILE,
            "workspace_id": 1,
            "connector_id": 1,
            "created_by_id": "00000000-0000-0000-0000-000000000001",
        }
        defaults.update(overrides)
        return ConnectorDocument(**defaults)

    return _make
