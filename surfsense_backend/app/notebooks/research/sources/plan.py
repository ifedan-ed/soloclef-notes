"""The shape every platform's search plan takes."""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from typing import Any

from pydantic import BaseModel

from app.capabilities.core import Capability

from ..schemas import ResearchSource, ResultRow


@dataclass(frozen=True)
class SourcePlan:
    """How one research source turns a query into result rows via a capability.

    ``build_input`` gets the query and how many results are wanted;
    ``to_rows`` reads the capability's typed output back as result rows.
    """

    capability: Capability
    build_input: Callable[[str, int], BaseModel]
    to_rows: Callable[[Any, ResearchSource], list[ResultRow]]
