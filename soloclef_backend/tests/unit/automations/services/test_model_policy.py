"""Lock the automation model policy: nothing is blocked.

Automations used to be restricted to billable models — a premium global config
or a user BYOK config — so that every unattended run could be charged to the
credit wallet. Billing is gone, so Auto mode, free globals and unset slots are
all ordinary choices. These tests pin that across the three model slots (chat
LLM, image, vision) and through both entry points, so a policy quietly
returning to life fails here rather than in a user's automation.
"""

from __future__ import annotations

from types import SimpleNamespace

import pytest

import app.automations.services.model_policy as model_policy
from app.automations.services.model_policy import (
    assert_automation_models_billable,
    assert_models_billable,
    get_automation_model_eligibility,
    get_model_eligibility,
)

pytestmark = pytest.mark.unit


def _workspace(*, llm: int | None, image: int | None, vision: int | None):
    """Minimal stand-in for the ``Workspace`` ORM row the policy reads."""
    return SimpleNamespace(
        chat_model_id=llm,
        image_gen_model_id=image,
        vision_model_id=vision,
    )


@pytest.mark.parametrize("kind", ["chat", "image", "vision"])
@pytest.mark.parametrize(
    "config_id",
    [
        pytest.param(7, id="byok"),
        pytest.param(0, id="auto-mode"),
        pytest.param(None, id="unset"),
        pytest.param(-1, id="premium-global"),
        pytest.param(-2, id="free-global"),
        pytest.param(-999, id="unknown-global"),
    ],
)
def test_every_model_id_is_allowed(kind: str, config_id) -> None:
    """Every id shape — BYOK, Auto mode, unset, premium, free, unknown."""
    allowed, reason = model_policy._classify(kind, config_id)
    assert allowed is True
    assert reason == ""


def test_eligibility_reports_no_violations() -> None:
    """The slot combination that used to produce three violations now passes."""
    workspace = _workspace(llm=-2, image=0, vision=-2)
    assert get_automation_model_eligibility(workspace) == {
        "allowed": True,
        "violations": [],
    }


def test_assert_never_raises_for_a_workspace() -> None:
    """Auto mode in a slot no longer stops an automation being created."""
    workspace = _workspace(llm=0, image=5, vision=-1)
    assert assert_automation_models_billable(workspace) is None


# --- ID-based core (used by the runtime backstop against captured snapshots) ---


def test_get_model_eligibility_reports_no_violations() -> None:
    """Explicit ids: free LLM, Auto image and free vision are all fine."""
    assert get_model_eligibility(
        chat_model_id=-2, image_gen_model_id=0, vision_model_id=-2
    ) == {"allowed": True, "violations": []}


def test_assert_models_billable_never_raises() -> None:
    """The runtime backstop lets a captured Auto-mode snapshot through."""
    assert (
        assert_models_billable(
            chat_model_id=0, image_gen_model_id=5, vision_model_id=-1
        )
        is None
    )


def test_workspace_wrapper_delegates_to_core() -> None:
    """The workspace wrapper produces the same result as the ID core."""
    workspace = _workspace(llm=-2, image=0, vision=-2)
    assert get_automation_model_eligibility(workspace) == get_model_eligibility(
        chat_model_id=-2, image_gen_model_id=0, vision_model_id=-2
    )
