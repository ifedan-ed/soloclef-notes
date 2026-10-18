"""The embedding settings as docker/.env spells them."""

from __future__ import annotations

import io
from pathlib import Path

import pytest
from dotenv import dotenv_values

from app.embedding_roles.settings import (
    DEFAULT_DOCUMENT_BATCH_SIZE,
    DEFAULT_DOCUMENT_BREAKER_SECONDS,
    DEFAULT_DOCUMENT_CONCURRENCY,
    DEFAULT_DOCUMENT_TIMEOUT_SECONDS,
    DEFAULT_QUERY_HOST_BATCH_SIZE,
    DEFAULT_QUERY_HOST_CONCURRENCY,
    load_embedding_role_settings,
)

pytestmark = pytest.mark.unit

LOCAL = "litellm://openai/local-bge-m3"
REMOTE = "litellm://openai/openrouter-bge-m3"


def _from_env_file(text: str):
    return load_embedding_role_settings(dotenv_values(stream=io.StringIO(text)))


def test_unset_means_one_model_for_both_and_the_documented_limits():
    settings = load_embedding_role_settings({"EMBEDDING_MODEL": LOCAL})

    assert settings.query_model == LOCAL
    assert settings.document_model == LOCAL
    assert settings.one_model
    assert not settings.document_has_own_host
    # The document model's own host: wide and quick to give up.
    assert settings.document_concurrency == DEFAULT_DOCUMENT_CONCURRENCY == 4
    assert settings.document_batch_size == DEFAULT_DOCUMENT_BATCH_SIZE == 32
    assert settings.document_timeout_seconds == DEFAULT_DOCUMENT_TIMEOUT_SECONDS == 20
    assert settings.document_breaker_seconds == DEFAULT_DOCUMENT_BREAKER_SECONDS == 300
    assert settings.document_fallback is True
    # The question model's server: one small batch at a time.
    assert settings.query_host_concurrency == DEFAULT_QUERY_HOST_CONCURRENCY == 1
    assert settings.query_host_batch_size == DEFAULT_QUERY_HOST_BATCH_SIZE == 8


def test_this_servers_lines_send_questions_local_and_documents_to_openrouter():
    settings = _from_env_file(
        f"EMBEDDING_MODEL={LOCAL}\n"
        f"EMBEDDING_QUERY_MODEL={LOCAL}\n"
        f"EMBEDDING_DOCUMENT_MODEL={REMOTE}\n"
    )

    assert settings.query_model == LOCAL
    assert settings.document_model == REMOTE
    assert not settings.one_model
    assert settings.document_has_own_host


@pytest.mark.parametrize(
    "switched_back",
    [
        # Delete the document line ...
        f"EMBEDDING_MODEL={LOCAL}\nEMBEDDING_QUERY_MODEL={LOCAL}\n",
        # ... or point it at the local host ...
        f"EMBEDDING_MODEL={LOCAL}\nEMBEDDING_QUERY_MODEL={LOCAL}\n"
        f"EMBEDDING_DOCUMENT_MODEL={LOCAL}\n",
        # ... or leave it empty.
        f"EMBEDDING_MODEL={LOCAL}\nEMBEDDING_QUERY_MODEL={LOCAL}\n"
        "EMBEDDING_DOCUMENT_MODEL=\n",
    ],
)
def test_documents_go_back_to_the_local_host_with_one_line(switched_back):
    settings = _from_env_file(switched_back)

    assert settings.document_model == LOCAL
    assert settings.one_model


def test_values_are_read_with_spaces_and_any_case():
    settings = load_embedding_role_settings(
        {
            "EMBEDDING_MODEL": f" {LOCAL} ",
            "EMBEDDING_DOCUMENT_MODEL": f" {REMOTE}",
            "EMBEDDING_DOCUMENT_CONCURRENCY": " 0 ",
            "EMBEDDING_DOCUMENT_BATCH_SIZE": "64",
            "EMBEDDING_DOCUMENT_TIMEOUT_SECONDS": " 7.5",
            "EMBEDDING_DOCUMENT_BREAKER_SECONDS": "0",
            "EMBEDDING_DOCUMENT_FALLBACK": "False",
            "EMBEDDING_QUERY_HOST_DOCUMENT_CONCURRENCY": "2",
            "EMBEDDING_QUERY_HOST_DOCUMENT_BATCH_SIZE": "16 ",
        }
    )

    assert settings.query_model == LOCAL
    assert settings.document_model == REMOTE
    assert settings.document_concurrency == 0  # no cap
    assert settings.document_batch_size == 64
    assert settings.document_timeout_seconds == 7.5
    assert settings.document_breaker_seconds == 0  # no breaker
    assert settings.document_fallback is False
    assert settings.query_host_concurrency == 2
    assert settings.query_host_batch_size == 16


def test_a_document_model_in_this_process_has_no_host_of_its_own():
    settings = load_embedding_role_settings(
        {
            "EMBEDDING_QUERY_MODEL": REMOTE,
            "EMBEDDING_DOCUMENT_MODEL": "sentence-transformers/all-MiniLM-L6-v2",
        }
    )

    assert not settings.one_model
    assert not settings.document_has_own_host


@pytest.mark.parametrize(
    ("name", "value", "says"),
    [
        ("EMBEDDING_DOCUMENT_CONCURRENCY", "two", "at least 0; unset, it is 4"),
        ("EMBEDDING_DOCUMENT_CONCURRENCY", "-1", "at least 0; unset, it is 4"),
        ("EMBEDDING_DOCUMENT_BATCH_SIZE", "0", "at least 1; unset, it is 32"),
        ("EMBEDDING_DOCUMENT_BATCH_SIZE", "8.5", "at least 1; unset, it is 32"),
        ("EMBEDDING_DOCUMENT_TIMEOUT_SECONDS", "0", "more than 0; unset, it is 20"),
        ("EMBEDDING_DOCUMENT_TIMEOUT_SECONDS", "soon", "more than 0; unset, it is 20"),
        ("EMBEDDING_DOCUMENT_TIMEOUT_SECONDS", "inf", "more than 0; unset, it is 20"),
        ("EMBEDDING_DOCUMENT_BREAKER_SECONDS", "-5", "0 or more; unset, it is 300"),
        ("EMBEDDING_DOCUMENT_BREAKER_SECONDS", "nan", "0 or more; unset, it is 300"),
        (
            "EMBEDDING_QUERY_HOST_DOCUMENT_CONCURRENCY",
            "-1",
            "at least 0; unset, it is 1",
        ),
        ("EMBEDDING_QUERY_HOST_DOCUMENT_BATCH_SIZE", "0", "at least 1; unset, it is 8"),
        (
            "EMBEDDING_DOCUMENT_FALLBACK",
            "maybe",
            "neither TRUE nor FALSE; unset, it is TRUE",
        ),
    ],
)
def test_an_unreadable_value_stops_the_server_naming_the_setting(name, value, says):
    with pytest.raises(ValueError) as refused:
        load_embedding_role_settings({"EMBEDDING_MODEL": LOCAL, name: value})

    assert name in str(refused.value)
    assert says in str(refused.value)


@pytest.mark.parametrize(
    "example", ["docker/.env.example", "surfsense_backend/.env.example"]
)
def test_the_example_env_files_keep_the_defaults(example):
    path = Path(__file__).resolve().parents[4] / example
    values = dotenv_values(path)

    settings = load_embedding_role_settings(values)

    assert settings.one_model
    assert settings.query_model == values["EMBEDDING_MODEL"]
    assert settings.document_concurrency == DEFAULT_DOCUMENT_CONCURRENCY
    assert settings.document_batch_size == DEFAULT_DOCUMENT_BATCH_SIZE
    assert settings.document_timeout_seconds == DEFAULT_DOCUMENT_TIMEOUT_SECONDS
    assert settings.document_breaker_seconds == DEFAULT_DOCUMENT_BREAKER_SECONDS
    assert settings.document_fallback is True
    assert settings.query_host_concurrency == DEFAULT_QUERY_HOST_CONCURRENCY
    assert settings.query_host_batch_size == DEFAULT_QUERY_HOST_BATCH_SIZE
