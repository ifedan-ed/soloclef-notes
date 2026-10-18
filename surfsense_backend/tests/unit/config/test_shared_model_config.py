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
def test_the_shared_list_is_seven_chat_and_six_image_models(shared_catalog):
    _, _, models = shared_catalog
    chat = [m for m in models if m["supports_chat"]]
    image = [m for m in models if m["supports_image_generation"]]
    assert len(chat) == 7 and len(image) == 6
    assert len({m["model_id"] for m in models}) == 13
    assert all(m["id"] < 0 for m in models)


@needs_config
def test_shared_models_carry_friendly_names_and_the_key_from_the_env(shared_catalog):
    _, connections, models = shared_catalog
    names = {m["model_id"]: m["display_name"] for m in models}
    assert names["ds-deepseek-v4.1-flash"] == "DeepSeek"
    assert names["openai-gpt-image-2"] == "GPT Image"
    assert names["openai-gpt-6-luna"] == "Luna Pro"
    assert {c["api_key"] for c in connections} == {"sk-test-shared-key"}
    assert {c["base_url"] for c in connections} == {"http://litellm:4000/v1"}
    assert all("os.environ" not in str(c) for c in connections)


@needs_config
def test_tiers_are_explicit_and_only_gpt_6_luna_is_premium(shared_catalog):
    _, _, models = shared_catalog
    tiers = {m["model_id"]: m["billing_tier"] for m in models}
    assert set(tiers.values()) == {"standard", "premium"}
    assert [k for k, v in tiers.items() if v == "premium"] == ["openai-gpt-6-luna"]


@needs_config
def test_the_removed_groq_qwen_models_are_not_offered(shared_catalog):
    _, _, models = shared_catalog
    assert not [m for m in models if m["model_id"].startswith("groq-qwen")]


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
