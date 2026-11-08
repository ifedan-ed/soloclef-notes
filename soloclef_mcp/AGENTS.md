# soloclef_mcp

MCP server. Talks to the Soloclef backend over REST only. Does not import backend code. Root `AGENTS.md` owns organization.

Layout: `mcp_server/features/` (scrapers, knowledge base, workspaces), `mcp_server/core/` (auth, client, transport).

## Commands

```bash
uv sync
uv run pytest
uv run soloclef-mcp
```

## Do

- New tools: vertical slice under `mcp_server/features/`, one responsibility per file.
- New behavior: `tdd` skill.
- Adding a new MCP tool shape later: install Anthropic `mcp-builder` then, not now.

## Do not

- Do not import `soloclef_backend`.
- Do not rewrite existing feature packages unless that is the task.
