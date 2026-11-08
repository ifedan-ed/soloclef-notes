"""Vision and image-generation defaults for a new workspace.

Chosen by the owner on 2026-09-25 as "my current defaults": his workspace used
Gemini 3.7 Flash to read images and GPT Image 2 to make them. A default whose
model is not in the shared list leaves the role on Auto (0), exactly as
before this existed.
"""

from __future__ import annotations

from typing import Any

from app.services.model_capabilities import has_capability

# workspace column -> (LiteLLM model id, capability it must have)
DEFAULT_ROLE_MODELS: dict[str, tuple[str, str]] = {
    "vision_model_id": ("google-gemini-3.7-flash", "vision"),
    "image_gen_model_id": ("openai-gpt-image-2", "image_gen"),
}


def default_role_model_ids(
    global_models: list[dict[str, Any]] | None = None,
) -> dict[str, int]:
    """Each defaulted role column and the shared model id it starts on."""
    if global_models is None:
        from app.config import config

        global_models = config.GLOBAL_MODELS or []
    chosen: dict[str, int] = {}
    for column, (model_name, capability) in DEFAULT_ROLE_MODELS.items():
        for model in global_models:
            if (
                model.get("model_id") == model_name
                and model.get("enabled", True)
                and int(model.get("id") or 0) < 0
                and has_capability(model, capability)
            ):
                chosen[column] = int(model["id"])
                break
    return chosen


def apply_default_role_models(
    workspace: Any, global_models: list[dict[str, Any]] | None = None
) -> None:
    """Point a new workspace's vision and image roles at the defaults."""
    for column, model_id in default_role_model_ids(global_models).items():
        setattr(workspace, column, model_id)
