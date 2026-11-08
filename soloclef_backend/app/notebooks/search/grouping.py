"""Fold ranked passages into per-notebook groups, within the result caps.

Pure: the service ranks passages once across every notebook, then this maps
each one back to the notebooks it belongs to. A document that is a source of
several notebooks appears under each of them.
"""

from __future__ import annotations

from dataclasses import dataclass, field

MAX_NOTEBOOKS = 8
MAX_PER_NOTEBOOK = 3
MAX_PASSAGES = 20


@dataclass(frozen=True)
class SourceLink:
    """How a document reaches a notebook: its own source row, or a folder's."""

    notebook_id: int
    source_id: int
    folder_name: str | None = None


@dataclass(frozen=True)
class Candidate:
    """One ranked passage, before it is placed under its notebooks."""

    kind: str  # "source" | "note"
    item_id: int  # document id or note id
    title: str
    content: str
    score: float
    document_type: str | None = None
    note_notebook_id: int | None = None


@dataclass
class Placed:
    candidate: Candidate
    link: SourceLink | None  # None for notes


@dataclass
class Group:
    notebook_id: int
    passages: list[Placed] = field(default_factory=list)


def group_passages(
    ranked: list[Candidate],
    links: dict[int, list[SourceLink]],
    *,
    max_notebooks: int = MAX_NOTEBOOKS,
    per_notebook: int = MAX_PER_NOTEBOOK,
    max_passages: int = MAX_PASSAGES,
) -> list[Group]:
    """Groups in order of each notebook's best passage.

    Two passes keep a notebook's few slots varied: first one passage per
    source or note, then any remaining slots take further passages from the
    same ones, still in rank order.
    """
    groups: dict[int, Group] = {}
    # (notebook, kind, item, rank index) of every passage placed so far.
    placed: set[tuple[int, str, int, int]] = set()
    used_items: set[tuple[int, str, int]] = set()
    total = 0

    def targets(candidate: Candidate) -> list[tuple[int, SourceLink | None]]:
        if candidate.kind == "note":
            if candidate.note_notebook_id is None:
                return []
            return [(candidate.note_notebook_id, None)]
        return [(link.notebook_id, link) for link in links.get(candidate.item_id, [])]

    for distinct_only in (True, False):
        for index, candidate in enumerate(ranked):
            for notebook_id, link in targets(candidate):
                if total >= max_passages:
                    break
                item_key = (notebook_id, candidate.kind, candidate.item_id)
                key = (*item_key, index)
                if key in placed:
                    continue
                if distinct_only and item_key in used_items:
                    continue
                group = groups.get(notebook_id)
                if group is None:
                    if len(groups) >= max_notebooks:
                        continue
                    group = groups[notebook_id] = Group(notebook_id)
                if len(group.passages) >= per_notebook:
                    continue
                group.passages.append(Placed(candidate, link))
                placed.add(key)
                used_items.add(item_key)
                total += 1
    # The second pass appends; restore rank order inside each notebook.
    rank = {id(candidate): i for i, candidate in enumerate(ranked)}
    for group in groups.values():
        group.passages.sort(key=lambda p: rank[id(p.candidate)])
    return list(groups.values())
