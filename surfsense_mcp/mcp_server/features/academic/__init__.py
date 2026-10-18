"""Academic literature search tools: PubMed, arXiv, medRxiv/bioRxiv, Scholar."""

from __future__ import annotations

from mcp.server.fastmcp import FastMCP

from ...core.client import SurfSenseClient
from ...core.workspace_context import WorkspaceContext
from . import tools


def register(mcp: FastMCP, client: SurfSenseClient, context: WorkspaceContext) -> None:
    """Register every academic search tool on the server."""
    tools.register(mcp, client, context)
