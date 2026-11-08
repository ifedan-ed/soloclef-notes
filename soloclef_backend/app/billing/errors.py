"""The one refusal billing raises, and its HTTP shape.

Every gate raises :class:`BillingDenied`. Routes turn it into HTTP 402 with a
body the web and mobile apps read the same way::

    {"detail": {"error_code": "INSUFFICIENT_CREDITS" | "PLAN_...",
                "message": "...", "action": "studio_video",
                "required_credits": 40, "available_credits": 12,
                "billing_path": "/billing"}}

Chat streams send the same ``error_code`` in their SSE error frame.
"""

from __future__ import annotations

from fastapi import HTTPException, status

from .entitlements import INSUFFICIENT_CREDITS
from .units import micros_to_credits

BILLING_PATH = "/billing"

# English fallbacks; the apps translate by error_code.
MESSAGES: dict[str, str] = {
    INSUFFICIENT_CREDITS: "You don't have enough credits for this. Top up or upgrade your plan.",
    "PLAN_PARSER_NOT_INCLUDED": "Cloud parsing isn't included in your plan. Upgrade to use it.",
    "PLAN_VISION_NOT_INCLUDED": "Image understanding isn't included in your plan. Upgrade to use it.",
    "PLAN_MODEL_TIER_NOT_INCLUDED": "Premium models aren't included in your plan. Upgrade or pick a standard model.",
    "PLAN_STUDIO_KIND_NOT_INCLUDED": "This Studio output isn't included in your plan. Upgrade to create it.",
    "PLAN_VIDEO_TOO_LONG": "Videos this long aren't included in your plan. Upgrade or choose a shorter video.",
    "PLAN_TOO_MANY_PAGES": "This upload has more pages than your plan allows. Upgrade or split the file.",
    "PLAN_TOO_MANY_JOBS": "Your plan's limit of Studio jobs running at once is reached. Wait for one to finish or upgrade.",
    "PLAN_TOO_MANY_NOTEBOOKS": "Your plan's notebook limit is reached. Upgrade to create more.",
    "PLAN_TOO_MANY_SOURCES": "This notebook has as many sources as your plan allows. Upgrade to add more.",
}


class BillingDenied(Exception):  # noqa: N818
    """An action refused by live billing: a plan rule or too few credits."""

    def __init__(
        self,
        code: str,
        *,
        action: str,
        required_micros: int = 0,
        available_micros: int = 0,
    ) -> None:
        self.code = code
        self.action = action
        self.required_micros = required_micros
        self.available_micros = available_micros
        super().__init__(MESSAGES.get(code, code))

    @property
    def message(self) -> str:
        return MESSAGES.get(self.code, self.code)

    def payload(self) -> dict:
        return {
            # "code"/"message" become the standard error envelope's fields.
            "code": self.code,
            "error_code": self.code,
            "message": self.message,
            "action": self.action,
            "required_credits": micros_to_credits(self.required_micros),
            "available_credits": micros_to_credits(max(0, self.available_micros)),
            "billing_path": BILLING_PATH,
        }

    def to_http(self) -> HTTPException:
        return HTTPException(
            status_code=status.HTTP_402_PAYMENT_REQUIRED, detail=self.payload()
        )
