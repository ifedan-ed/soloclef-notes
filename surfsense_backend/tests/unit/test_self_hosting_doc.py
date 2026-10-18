"""What the self-hosters' guide (surfsense_web/content/docs/self-hosting.mdx)
must say about this backend, where a missing sentence costs a server its
pushes or lets strangers in. The code facts behind each sentence:
app/push/server_origin.py (push names SURFSENSE_PUBLIC_URL, https only) and
app.py / app/routes/auth_routes.py (Google sign-in makes accounts)."""

from __future__ import annotations

import re
from pathlib import Path

import pytest

pytestmark = pytest.mark.unit

DOC = (
    Path(__file__).resolve().parents[3]
    / "surfsense_web"
    / "content"
    / "docs"
    / "self-hosting.mdx"
)


def _section(title: str) -> str:
    """The text under a ``##``/``###`` heading, up to the next heading of any level."""
    if not DOC.exists():
        pytest.skip(
            "surfsense_web/content/docs/self-hosting.mdx is not in this checkout"
        )
    text = DOC.read_text()
    match = re.search(
        rf"^#{{2,3}} {re.escape(title)}\n(.*?)(?=^#{{2,3}} |\Z)", text, re.S | re.M
    )
    assert match, f"self-hosting.mdx has no section {title!r}"
    # One line per paragraph, so a phrase split across lines still matches.
    return " ".join(match.group(1).split())


def test_the_phone_app_section_says_push_needs_the_https_address_people_type():
    text = _section("The phone app")
    assert "`PUSH_ENABLED=TRUE`" in text
    assert "need `SURFSENSE_PUBLIC_URL` to be the https address people type" in text
    # The two ways it goes wrong: another address drops every push; http or an
    # address the app refuses names no server, and the log says so.
    assert "their phones drop every push" in text
    assert "A plain-http address" in text
    assert "`[startup] push: ...`" in text
    assert "`xn--bcher-kva.example`" in text


def test_closing_sign_ups_says_google_makes_an_account_for_anyone():
    text = _section("Closing sign-ups")
    # Both Google ways in make accounts (resolve_google_user, fastapi-users'
    # oauth_callback); no setting and no proxy rule can refuse only new ones.
    for name in (
        "`AUTH_TYPE=GOOGLE`",
        "`/auth/google/callback`",
        "`/auth/desktop/session`",
        "`GOOGLE_DESKTOP_CLIENT_ID`",
    ):
        assert name in text, name
    assert "Turn Google sign-in off: `AUTH_TYPE=LOCAL`" in text
    assert "the proxy cannot do it" in text


def test_the_embeddings_section_names_each_setting_its_default_and_limits():
    from app.embedding_roles import (
        dimension_agreement,
        document_embedding,
        document_slots,
        redis_link,
        settings,
    )

    text = _section("Embeddings: questions and documents")
    defaults = {
        settings.QUERY_MODEL_ENV: "`EMBEDDING_MODEL`",
        settings.DOCUMENT_MODEL_ENV: "`EMBEDDING_MODEL`",
        settings.DOCUMENT_CONCURRENCY_ENV: f"`{settings.DEFAULT_DOCUMENT_CONCURRENCY}`",
        settings.DOCUMENT_BATCH_SIZE_ENV: f"`{settings.DEFAULT_DOCUMENT_BATCH_SIZE}`",
        settings.DOCUMENT_TIMEOUT_ENV: f"`{settings.DEFAULT_DOCUMENT_TIMEOUT_SECONDS:g}`",
        settings.DOCUMENT_BREAKER_ENV: f"`{settings.DEFAULT_DOCUMENT_BREAKER_SECONDS:g}`",
        settings.DOCUMENT_FALLBACK_ENV: "`TRUE`",
        settings.QUERY_HOST_CONCURRENCY_ENV: f"`{settings.DEFAULT_QUERY_HOST_CONCURRENCY}`",
        settings.QUERY_HOST_BATCH_SIZE_ENV: f"`{settings.DEFAULT_QUERY_HOST_BATCH_SIZE}`",
    }
    for name, default in defaults.items():
        assert f"| `{name}` | {default} |" in text, name
    # The re-index rule.
    assert "with **no re-indexing**" in text
    assert (
        "A different model (another name, version or size) is not a settings change"
        in text
    )
    assert "refuses to start if the lengths differ" in text
    # The limits app/embedding_roles holds, each as the code has it.
    assert "Celery beat, and the migrations" in text
    assert (
        f"at most {dimension_agreement.PROBE_TIMEOUT_SECONDS:g} seconds for each"
        in text
    )
    assert f"within {document_slots.SLOT_WAIT_SECONDS / 60:.0f} minutes" in text
    assert f"a {document_slots.SLOT_LEASE_SECONDS:.0f}-second lease" in text
    assert f"renews every {document_slots.SLOT_RENEW_SECONDS:.0f} seconds" in text
    poll = document_slots._POLL_SECONDS
    assert f"every {poll:.2f} to {poll * 1.5:.2f} seconds" in text
    assert f"waits at most {redis_link.REDIS_TIMEOUT_SECONDS:.0f} seconds" in text
    assert redis_link.REDIS_RETRY_SECONDS == 60
    assert "left alone for a minute" in text
    assert document_embedding.UNCAPPED_PARALLEL_BATCHES == 4
    assert "still sends at most 4 at once" in text
    assert "`embedding:document_breaker:<model>`" in text
    assert "`embedding:document_slot:<model>:<n>`" in text
    assert "takes no slot" in text
    assert "`[startup] embeddings:" in text
    # Gone: the 60 s a request, and the fallback inside the document's slot.
    assert "may take 60 seconds" not in text
    assert "does not add to what the question model's server is asked" not in text


def test_moving_documents_back_is_one_line():
    text = _section("Moving documents back to the questions' host")
    assert "delete the `EMBEDDING_DOCUMENT_MODEL` line" in text
    assert (
        "`docker compose -f docker-compose.yml up -d backend celery_worker celery_beat`"
        in text
    )
    assert "If you keep your own override file, add it after the first" in text
    assert "Nothing is re-indexed" in text


def test_this_servers_switch_back_gives_its_own_compose_files():
    """custom/README.md (this server's notes) names the override file the
    generic page leaves to each server, after the deploy gate."""
    readme = DOC.parents[3] / "custom" / "README.md"
    if not readme.exists():
        pytest.skip("custom/README.md is not in this checkout")
    text = readme.read_text()
    section = text[text.index("**Switching documents back to local later**") :]
    section = section[: section.index("\n## ")]
    assert "python3 surfsense_backend/scripts/deploy_gate.py --wait && \\\n" in section
    assert (
        "docker compose -f docker-compose.yml -f docker-compose.override.yml "
        "up -d backend celery_worker celery_beat"
    ) in section
    assert "No re-index" in section
