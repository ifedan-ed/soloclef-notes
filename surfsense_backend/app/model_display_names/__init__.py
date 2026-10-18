"""Friendly names for the model ids LiteLLM serves, shown instead of the raw id.

``names.py`` is the single source of truth: one row per LiteLLM model id.
Nothing here changes what is sent to LiteLLM; only what people read.
"""

from .names import FRIENDLY_NAMES, friendly_name

__all__ = ["FRIENDLY_NAMES", "friendly_name"]
