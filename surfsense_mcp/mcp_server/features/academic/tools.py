"""PubMed, arXiv, medRxiv/bioRxiv and Google Scholar search, standalone.

Thin wrappers over the backend's ``/workspaces/{id}/academic-search/{source}``
door (see ``surfsense_backend/app/notebooks/research/academic_routes.py``),
which reuses the notebook research academic sources without creating a
notebook or an import. Not billed (matches the backend today).
"""

from __future__ import annotations

from typing import Annotated, Literal

from mcp.server.fastmcp import FastMCP
from pydantic import Field

from ...core.client import SurfSenseClient
from ...core.rendering import ResponseFormatParam, to_json
from ...core.workspace_context import WorkspaceContext, WorkspaceParam
from .annotations import READ

MaxResults = Annotated[
    int, Field(ge=1, le=30, description="Maximum papers to return.")
]
Query = Annotated[str, Field(min_length=1, description="Search query.")]


def register(mcp: FastMCP, client: SurfSenseClient, context: WorkspaceContext) -> None:
    """Register the four academic search tools."""

    async def _search(
        workspace: str | None,
        source: str,
        body: dict,
    ) -> list[dict]:
        resolved = await context.resolve(workspace)
        result = await client.request(
            "POST",
            f"/workspaces/{resolved.id}/academic-search/{source}",
            json=body,
        )
        return result or []

    @mcp.tool(
        name="soloclef_pubmed_search",
        title="Search PubMed",
        annotations=READ,
        structured_output=False,
    )
    async def pubmed_search(
        query: Query,
        max_results: MaxResults = 10,
        since: Annotated[
            int | None, Field(description="Earliest publication year, inclusive.")
        ] = None,
        until: Annotated[
            int | None, Field(description="Latest publication year, inclusive.")
        ] = None,
        full_text: Annotated[
            bool,
            Field(
                description="When true, read PMC open-access full text for the "
                "top hits (where available) and return an excerpt instead of "
                "just the abstract."
            ),
        ] = False,
        workspace: WorkspaceParam = None,
        response_format: ResponseFormatParam = "markdown",
    ) -> str:
        """Search PubMed (biomedical literature) by keyword.

        Returns, per paper: title, authors, year, journal, PMID/PMCID/DOI, the
        PubMed URL, and an abstract (or a full-text excerpt when
        full_text=True and the paper is in PMC's open-access subset).
        Example: query='bronchiolitis high flow nasal cannula', since=2018.
        """
        rows = await _search(
            workspace,
            "pubmed",
            {
                "query": query,
                "max_results": max_results,
                "since": since,
                "until": until,
                "full_text": full_text,
            },
        )
        if response_format == "json":
            return to_json(rows)
        return _render(query, rows)

    @mcp.tool(
        name="soloclef_arxiv_search",
        title="Search arXiv",
        annotations=READ,
        structured_output=False,
    )
    async def arxiv_search(
        query: Query,
        max_results: MaxResults = 10,
        workspace: WorkspaceParam = None,
        response_format: ResponseFormatParam = "markdown",
    ) -> str:
        """Search arXiv (preprints: physics, CS, math, stats, and more).

        Returns, per paper: title, authors, year, arXiv id, DOI/journal-ref
        when published, the arXiv URL, and the abstract.
        Example: query='transformer attention mechanism'.
        """
        rows = await _search(
            workspace, "arxiv", {"query": query, "max_results": max_results}
        )
        if response_format == "json":
            return to_json(rows)
        return _render(query, rows)

    @mcp.tool(
        name="soloclef_preprint_search",
        title="Search medRxiv / bioRxiv",
        annotations=READ,
        structured_output=False,
    )
    async def preprint_search(
        query: Query,
        server: Annotated[
            Literal["medrxiv", "biorxiv"],
            Field(description="Which preprint server to search."),
        ],
        max_results: MaxResults = 10,
        workspace: WorkspaceParam = None,
        response_format: ResponseFormatParam = "markdown",
    ) -> str:
        """Search medRxiv (health sciences) or bioRxiv (biology) preprints.

        These are NOT peer reviewed. Returns, per preprint: title, authors,
        year, DOI, the landing-page URL, and the abstract.
        Example: query='pediatric asthma exacerbation', server='medrxiv'.
        """
        rows = await _search(
            workspace,
            "medrxiv",
            {"query": query, "max_results": max_results, "server": server},
        )
        if response_format == "json":
            return to_json(rows)
        return _render(query, rows)

    @mcp.tool(
        name="soloclef_scholar_search",
        title="Search Google Scholar",
        annotations=READ,
        structured_output=False,
    )
    async def scholar_search(
        query: Query,
        max_results: MaxResults = 10,
        workspace: WorkspaceParam = None,
        response_format: ResponseFormatParam = "markdown",
    ) -> str:
        """Search Google Scholar across disciplines and publishers.

        Broadest coverage, but the least structured result (no reliable
        abstract or full text — only the search snippet). Prefer
        soloclef_pubmed_search, soloclef_arxiv_search or
        soloclef_preprint_search when the topic fits one of them.
        Returns, per hit: title, authors/venue/year line, URL, cited-by count
        when shown, and the snippet.
        Example: query='pediatric sepsis early recognition'.
        """
        rows = await _search(
            workspace, "google_scholar", {"query": query, "max_results": max_results}
        )
        if response_format == "json":
            return to_json(rows)
        return _render(query, rows)


def _render(query: str, rows: list[dict]) -> str:
    if not rows:
        return f'No results for "{query}".'
    lines = [f'# {len(rows)} result(s) for "{query}"', ""]
    for row in rows:
        paper = row.get("paper") or {}
        lines.append(f"## {row.get('title', 'Untitled')}")
        if row.get("meta"):
            lines.append(row["meta"])
        ids = _identifiers(paper)
        if ids:
            lines.append(ids)
        if row.get("url"):
            lines.append(row["url"])
        snippet = (row.get("snippet") or "").strip()
        if snippet:
            lines.append(f"> {snippet}")
        lines.append("")
    return "\n".join(lines).strip()


def _identifiers(paper: dict) -> str:
    parts = []
    for key, label in (
        ("doi", "DOI"),
        ("pmid", "PMID"),
        ("pmcid", "PMCID"),
        ("arxiv_id", "arXiv"),
    ):
        if paper.get(key):
            parts.append(f"{label}: {paper[key]}")
    return " · ".join(parts)
