"""What a successful fetch hands back to the import job."""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(frozen=True)
class FetchedContent:
    markdown: str
    title: str | None = None
    metadata: dict[str, str] = field(default_factory=dict)
