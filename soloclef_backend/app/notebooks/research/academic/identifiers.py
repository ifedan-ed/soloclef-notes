"""When two paper rows are the same paper: a shared DOI, PMID, PMC id or arXiv id."""

from __future__ import annotations

from ..schemas import ResultRow
from .paper import PaperRef


def paper_keys(paper: PaperRef) -> set[str]:
    keys = set()
    if paper.doi:
        keys.add(f"doi:{paper.doi}")
    if paper.pmid:
        keys.add(f"pmid:{paper.pmid}")
    if paper.pmcid:
        keys.add(f"pmc:{paper.pmcid}")
    if paper.arxiv_id:
        keys.add(f"arxiv:{paper.arxiv_id}")
    return keys


def _merged(first: PaperRef, other: PaperRef) -> PaperRef:
    """``first`` keeps its facts; it gains ids and badges only ``other`` knew."""
    update = {
        field: getattr(other, field)
        for field in ("doi", "pmid", "pmcid", "arxiv_id", "pdf_url")
        if getattr(first, field) is None and getattr(other, field) is not None
    }
    update["badges"] = list(dict.fromkeys([*first.badges, *other.badges]))
    update["open_access"] = first.open_access or other.open_access
    return first.model_copy(update=update)


def dedupe_papers(rows: list[ResultRow]) -> list[ResultRow]:
    """First occurrence wins; later rows for the same paper fold into it."""
    kept: list[ResultRow] = []
    owner: dict[str, int] = {}
    for row in rows:
        keys = paper_keys(row.paper) if row.paper else set()
        index = next((owner[k] for k in keys if k in owner), None)
        if index is None:
            owner.update({k: len(kept) for k in keys})
            kept.append(row)
            continue
        first = kept[index]
        merged = _merged(first.paper, row.paper)
        kept[index] = first.model_copy(update={"paper": merged})
        owner.update(dict.fromkeys(paper_keys(merged), index))
    return kept
