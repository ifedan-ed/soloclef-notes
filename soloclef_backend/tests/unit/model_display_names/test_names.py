"""Friendly model names: one table, used for every shared model."""

from __future__ import annotations

from pathlib import Path

import pytest
import yaml

from app.model_display_names import FRIENDLY_NAMES, friendly_name

pytestmark = pytest.mark.unit

SHARED_CONFIG = (
    Path(__file__).resolve().parents[4] / "docker" / "global_llm_config.yaml"
)


# The owner's names, 2026-09-25: short family names, a version only where two
# of a family are offered, "Pro"/"Lite" for tiers.
OWNER_NAMES = {
    "ds-deepseek-v4.1-flash": "DeepSeek",
    "mistral-medium": "Mistral",
    "openrouter-llama-3.3-70b-instruct": "Llama",
    # Added 2026-09-26: Groq's two Qwen models, a version each (two of a family).
    "groq-qwen3.8-27b": "Qwen 3.8",
    "groq-qwen3.6-27b": "Qwen 3.6",
    "openai-gpt-5.6-luna": "Luna",
    "openai-gpt-6-luna": "Luna Pro",
    "google-gemini-3.7-flash": "Gemini 3.7",
    "google-gemini-3.8-flash": "Gemini 3.8",
    "openai-gpt-image-2": "GPT Image",
    "openai-gpt-image-2.5-sunburst": "Sunburst",
    "google-gemini-3.1-flash-image": "Gemini Image",
    "google-gemini-3.1-flash-lite-image": "Gemini Image Lite",
    "google-gemini-3-pro-image": "Gemini Image Pro",
    "openrouter-mai-image-2.5-pro": "MAI Image",
    "openrouter-bge-m3": "Embeddings",
}


def test_every_model_has_the_owners_name():
    assert FRIENDLY_NAMES == OWNER_NAMES
    for model_id, name in OWNER_NAMES.items():
        assert friendly_name(model_id) == name


def test_a_provider_prefix_is_ignored_for_the_lookup():
    assert friendly_name("openai/google-gemini-3.7-flash") == "Gemini 3.7"


def test_the_groq_qwen_models_are_named_by_version():
    assert friendly_name("groq-qwen3.8-27b") == "Qwen 3.8"
    assert friendly_name("groq-qwen3.6-27b") == "Qwen 3.6"
    # LiteLLM reports a served model with its provider prefix.
    assert friendly_name("openai/groq-qwen3.8-27b") == "Qwen 3.8"


def test_an_unlisted_id_is_shown_as_itself():
    assert friendly_name("someone-elses-model") == "someone-elses-model"
    assert friendly_name("") == ""
    assert friendly_name(None) == ""


def test_the_table_covers_every_enabled_model():
    # 15 rows were enabled on 2026-09-25 (14 distinct ids, gpt-6-luna twice);
    # the two Groq Qwen models joined on 2026-09-26.
    assert len(FRIENDLY_NAMES) == 16
    assert all(name.strip() and name != model for model, name in FRIENDLY_NAMES.items())
    assert len(set(FRIENDLY_NAMES.values())) == len(FRIENDLY_NAMES)


@pytest.mark.skipif(not SHARED_CONFIG.exists(), reason="docker/ not in this checkout")
def test_every_shared_model_has_a_friendly_name():
    data = yaml.safe_load(SHARED_CONFIG.read_text())
    ids = [
        entry["model_name"]
        for section in ("global_llm_configs", "global_image_generation_configs")
        for entry in data[section]
    ]
    assert ids and all(model in FRIENDLY_NAMES for model in ids)
