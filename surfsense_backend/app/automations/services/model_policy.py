"""Model policy for automations.

Nothing is blocked. Automations used to be restricted to a premium global model
or a user-provided BYOK model, so that every unattended run could be billed
against the credit wallet; with billing gone, Auto mode and free global models
are ordinary choices and every slot is allowed.

The module is kept rather than deleted because it is the single seam both
creation-time enforcement (``AutomationService.create`` and the
``create_automation`` chat tool) and the runtime backstop (``agent_task``
dependencies) call through — and because it is where a future policy, if one is
ever wanted, would go.
"""

from __future__ import annotations

from typing import TYPE_CHECKING, Literal

if TYPE_CHECKING:
    from app.db import Workspace

ModelKind = Literal["chat", "image", "vision"]

def _classify(kind: ModelKind, model_id: int | None) -> tuple[bool, str]:
    """Classify a resolved model id as allowed or blocked.

    Returns ``(allowed, reason)``; ``reason`` is empty when allowed.

    Every model is allowed. The rule this used to enforce — that an automation
    must run on a premium or BYOK model so the run could be billed — only made
    sense while runs were billed. Auto mode and free global models are ordinary
    choices now.
    """
    del kind, model_id
    return True, ""


def get_model_eligibility(
    *,
    chat_model_id: int | None,
    image_gen_model_id: int | None,
    vision_model_id: int | None,
) -> dict:
    """Return ``{"allowed": bool, "violations": [...]}`` for explicit model ids.

    The ID-based core shared by both the workspace path (creation/eligibility)
    and the captured-snapshot path (runtime backstop). Each violation is
    ``{"kind", "model_id", "reason"}``.
    """
    checks: list[tuple[ModelKind, int | None]] = [
        ("chat", chat_model_id),
        ("image", image_gen_model_id),
        ("vision", vision_model_id),
    ]

    violations: list[dict] = []
    for kind, model_id in checks:
        allowed, reason = _classify(kind, model_id)
        if not allowed:
            violations.append({"kind": kind, "model_id": model_id, "reason": reason})

    return {"allowed": not violations, "violations": violations}


def get_automation_model_eligibility(workspace: Workspace) -> dict:
    """Return ``{"allowed": bool, "violations": [...]}`` for a workspace.

    Used by the eligibility endpoint and the chat tool's early check. Thin
    wrapper over :func:`get_model_eligibility`.
    """
    return get_model_eligibility(
        chat_model_id=workspace.chat_model_id,
        image_gen_model_id=workspace.image_gen_model_id,
        vision_model_id=workspace.vision_model_id,
    )


class AutomationModelPolicyError(Exception):
    """Raised when a workspace's models are not billable for automations."""

    def __init__(self, violations: list[dict]) -> None:
        self.violations = violations
        reasons = "; ".join(v["reason"] for v in violations)
        super().__init__(
            reasons or "Automations require premium or BYOK models for all model slots."
        )


def assert_models_billable(
    *,
    chat_model_id: int | None,
    image_gen_model_id: int | None,
    vision_model_id: int | None,
) -> None:
    """Raise :class:`AutomationModelPolicyError` if any explicit id is not billable.

    The ID-based core used by the runtime backstop against an automation's
    captured model snapshot.
    """
    result = get_model_eligibility(
        chat_model_id=chat_model_id,
        image_gen_model_id=image_gen_model_id,
        vision_model_id=vision_model_id,
    )
    if not result["allowed"]:
        raise AutomationModelPolicyError(result["violations"])


def assert_automation_models_billable(workspace: Workspace) -> None:
    """Raise :class:`AutomationModelPolicyError` if any model slot is not billable."""
    result = get_automation_model_eligibility(workspace)
    if not result["allowed"]:
        raise AutomationModelPolicyError(result["violations"])
