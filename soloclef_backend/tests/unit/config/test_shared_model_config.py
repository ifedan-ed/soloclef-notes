"""The shared model list (docker/global_llm_config.yaml) as the app loads it."""

from __future__ import annotations

import shutil
from pathlib import Path

import pytest

import app.config as app_config_module
from app.config import load_global_image_gen_configs, load_global_llm_configs
from app.config.env_references import resolve_env_reference
from app.services.global_model_catalog import materialize_global_model_catalog

pytestmark = pytest.mark.unit

SHARED_CONFIG = (
    Path(__file__).resolve().parents[4] / "docker" / "global_llm_config.yaml"
)
needs_config = pytest.mark.skipif(
    not SHARED_CONFIG.exists(), reason="docker/ not in this checkout"
)


def test_an_env_reference_reads_the_variable(monkeypatch):
    monkeypatch.setenv("SOME_KEY", " sk-abc ")
    assert resolve_env_reference("os.environ/SOME_KEY") == "sk-abc"


def test_an_unset_reference_is_no_key_not_the_literal_text(monkeypatch):
    monkeypatch.delenv("MISSING_KEY", raising=False)
    assert resolve_env_reference("os.environ/MISSING_KEY") is None
    assert resolve_env_reference("os.environ/") is None


def test_plain_values_are_left_alone():
    assert resolve_env_reference("sk-literal") == "sk-literal"
    assert resolve_env_reference(None) is None
    assert resolve_env_reference(5) == 5


@pytest.fixture
def shared_catalog(tmp_path, monkeypatch):
    config_dir = tmp_path / "app" / "config"
    config_dir.mkdir(parents=True)
    shutil.copyfile(SHARED_CONFIG, config_dir / "global_llm_config.yaml")
    monkeypatch.setattr(app_config_module, "BASE_DIR", tmp_path)
    monkeypatch.setenv("OPENAI_API_KEY", "sk-test-shared-key")
    chat = load_global_llm_configs()
    image = load_global_image_gen_configs()
    connections, models = materialize_global_model_catalog(
        chat_configs=chat, image_configs=image
    )
    return chat, connections, models


@needs_config
def test_the_shared_list_loads_whole(shared_catalog):
    """Whatever the owner offers, every entry loads: chat or image, a unique
    model id, a negative (shared) id. The list itself is the owner's to
    change, so no count is pinned here."""
    _, _, models = shared_catalog
    assert models
    assert all(m["supports_chat"] or m["supports_image_generation"] for m in models)
    assert len({m["model_id"] for m in models}) == len(models)
    assert all(m["id"] < 0 for m in models)


@needs_config
def test_shared_models_carry_a_name_and_the_key_from_the_env(shared_catalog):
    _, connections, models = shared_catalog
    assert all(m["display_name"] for m in models)
    assert {c["api_key"] for c in connections} == {"sk-test-shared-key"}
    assert all("os.environ" not in str(c) for c in connections)


@needs_config
def test_every_tier_is_explicit(shared_catalog):
    _, _, models = shared_catalog
    assert {m["billing_tier"] for m in models} <= {"standard", "premium"}


@needs_config
def test_the_owners_defaults_are_all_in_the_shared_list(shared_catalog):
    from app.quick_tasks.resolution import DEFAULT_QUICK_MODEL_ID
    from app.studio_models.recommended import RECOMMENDED_DEFAULT
    from app.workspace_model_defaults import DEFAULT_ROLE_MODELS

    _, _, models = shared_catalog
    ids = {m["model_id"] for m in models}
    wanted = {DEFAULT_QUICK_MODEL_ID, *RECOMMENDED_DEFAULT} | {
        name for name, _ in DEFAULT_ROLE_MODELS.values()
    }
    assert wanted - {None} <= ids
