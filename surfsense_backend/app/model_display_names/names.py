"""The name each shared model is shown under.

Keyed by the exact LiteLLM model id (what SurfSense sends as ``model``).
Names are short on purpose (the owner's choice, 2026-09-25): a family name,
a version only where two of a family are offered, and "Pro"/"Lite" for tiers.
Edit a name here and redeploy to rename a model everywhere it is shown; the
table is mirrored in ``custom/README.md``. An id that is not listed is shown
as itself.
"""

from __future__ import annotations

FRIENDLY_NAMES: dict[str, str] = {
    # Chat
    "ds-deepseek-v4.1-flash": "DeepSeek",
    "openai-gpt-5.6-luna": "Luna",
    "openai-gpt-6-luna": "Luna Pro",
    "google-gemini-3.7-flash": "Gemini 3.7",
    "google-gemini-3.8-flash": "Gemini 3.8",
    "mistral-medium": "Mistral",
    "openrouter-llama-3.3-70b-instruct": "Llama",
    "groq-qwen3.8-27b": "Qwen 3.8",
    "groq-qwen3.6-27b": "Qwen 3.6",
    # Image generation
    "openai-gpt-image-2": "GPT Image",
    "openai-gpt-image-2.5-sunburst": "Sunburst",
    "google-gemini-3.1-flash-image": "Gemini Image",
    "google-gemini-3.1-flash-lite-image": "Gemini Image Lite",
    "google-gemini-3-pro-image": "Gemini Image Pro",
    "openrouter-mai-image-2.5-pro": "MAI Image",
    # Embeddings (not user-selectable; named for the admin pages)
    "openrouter-bge-m3": "Embeddings",
}


def friendly_name(model_id: str | None) -> str:
    """The name to show for ``model_id``: its friendly name, else the id itself.

    A provider prefix (``openai/ds-deepseek-v4.1-flash``, as LiteLLM reports a
    served model) is ignored for the lookup.
    """
    raw = (model_id or "").strip()
    if not raw:
        return ""
    if raw in FRIENDLY_NAMES:
        return FRIENDLY_NAMES[raw]
    bare = raw.rsplit("/", 1)[-1]
    return FRIENDLY_NAMES.get(bare, raw)
