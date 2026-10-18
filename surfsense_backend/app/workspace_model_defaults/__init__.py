"""The models a new workspace starts on: the owner's own choices, for everyone.

Chat starts on Auto (the column default). Vision and image generation start
on the models named in ``defaults.py``, matched against the shared (global)
models by LiteLLM id. The Studio outputs model and the quick-tasks model
already default by name (``app.studio_models.recommended``,
``app.quick_tasks.resolution``), so a workspace that never touched its
settings runs Studio and quick tasks exactly as the owner's does.
"""

from .defaults import (
    DEFAULT_ROLE_MODELS,
    apply_default_role_models,
    default_role_model_ids,
)

__all__ = ["DEFAULT_ROLE_MODELS", "apply_default_role_models", "default_role_model_ids"]
