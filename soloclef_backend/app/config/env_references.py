"""``os.environ/NAME`` values in ``global_llm_config.yaml``, read from the environment.

The same spelling LiteLLM's own config uses, so the shared model list can be
committed while its key stays in ``docker/.env``. A reference to an unset or
empty variable resolves to ``None`` (the model then has no key) rather than
to the literal text, which would otherwise be sent upstream as a key.
"""

from __future__ import annotations

import os
from typing import Any

PREFIX = "os.environ/"

# Only these fields may name an environment variable; a model name or a
# prompt that happens to start with the prefix stays as written.
REFERENCE_FIELDS = ("api_key", "api_base")


def resolve_env_reference(value: Any) -> Any:
    if not isinstance(value, str) or not value.startswith(PREFIX):
        return value
    name = value[len(PREFIX) :].strip()
    if not name:
        return None
    return (os.environ.get(name) or "").strip() or None


def resolve_config_env_references(cfg: dict[str, Any]) -> dict[str, Any]:
    """Resolve the reference fields of one config entry in place."""
    for field in REFERENCE_FIELDS:
        if field in cfg:
            cfg[field] = resolve_env_reference(cfg[field])
    return cfg
