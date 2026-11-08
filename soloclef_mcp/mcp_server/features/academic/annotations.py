"""Tool-call policy hints shared by the academic search tools."""

from __future__ import annotations

from mcp.types import ToolAnnotations

READ = ToolAnnotations(
    readOnlyHint=True, destructiveHint=False, idempotentHint=True, openWorldHint=True
)
