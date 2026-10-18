"""What a workspace can choose, for the default and for each output type.

``recommended`` is only offered for the default: it is the measured setup in
``recommended``. ``auto`` is today's behaviour, the workspace chat model (or
its Auto pin). ``model`` is a specific model role id with an optional
fallback. An output type with no entry in ``overrides`` uses the default.
"""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict, model_validator

from .slots import StudioSlot


class StudioChoice(BaseModel):
    model_config = ConfigDict(extra="forbid")

    mode: Literal["recommended", "auto", "model"]
    primary: int | None = None
    fallback: int | None = None

    @model_validator(mode="after")
    def _ids_only_for_a_model(self) -> StudioChoice:
        if self.mode != "model":
            if self.primary is not None or self.fallback is not None:
                raise ValueError(f"'{self.mode}' does not take model ids")
            return self
        if not self.primary:
            raise ValueError("a specific model needs a primary model id")
        if self.fallback == 0:
            self.fallback = None
        if self.fallback is not None and self.fallback == self.primary:
            raise ValueError("the fallback must differ from the primary model")
        return self

    def model_ids(self) -> list[int]:
        return [i for i in (self.primary, self.fallback) if i]


class StudioModelSettings(BaseModel):
    model_config = ConfigDict(extra="forbid")

    default: StudioChoice
    overrides: dict[StudioSlot, StudioChoice] = {}

    @model_validator(mode="after")
    def _recommended_is_only_a_default(self) -> StudioModelSettings:
        for slot, choice in self.overrides.items():
            if choice.mode == "recommended":
                raise ValueError(f"{slot}: choose Auto or a model, or leave it out")
        return self
