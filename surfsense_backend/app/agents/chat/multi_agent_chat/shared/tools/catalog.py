"""Pure-data catalog of built-in agent tools.

This module advertises *what* tools exist and their display metadata. It is
intentionally free of any tool implementation imports (no connectors, no
factories) so it can be consumed without pulling the whole tool dependency
graph — and so connector packages stay independently deletable.

The single live consumer is the ``GET /agent/tools`` endpoint, which renders
the tool picker in the web UI. Tool *construction* lives elsewhere:

* main-agent tools  -> ``app.agents.chat.multi_agent_chat.main_agent.tools.registry``
* subagent / connector tools -> ``app.agents.chat.multi_agent_chat.subagents.*``
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class ToolMetadata:
    """Display metadata for a single built-in tool.

    Attributes:
        name: Unique identifier for the tool.
        description: Human-readable description of what the tool does.
        enabled_by_default: Whether the tool is on when no explicit config
            is provided.
        hidden: WIP tools that should be excluded from public listings.

    """

    name: str
    description: str
    enabled_by_default: bool = True
    hidden: bool = False


# Catalog of all built-in tools. Contributors: add new tools here so they show
# up in the UI tool picker. This list carries metadata only — wire the actual
# implementation in the relevant builder/registry module.
TOOL_CATALOG: list[ToolMetadata] = [
    ToolMetadata(
        name="generate_podcast",
        description="Generate an audio podcast from provided content",
    ),
    ToolMetadata(
        name="generate_video_presentation",
        description="Generate narrated audiovisual presentation media",
    ),
    ToolMetadata(
        name="save_artifact",
        description="Save a Markdown or sandbox-generated file as a durable artifact",
    ),
    ToolMetadata(
        name="load_artifact_for_revision",
        description="Load an artifact's current deliverable and context for revision",
    ),
    ToolMetadata(
        name="execute",
        description="Run Python or Bash in an isolated artifact sandbox",
    ),
    ToolMetadata(
        name="load_artifact_instructions",
        description="Load trusted creation instructions for an artifact format",
        hidden=True,
    ),
    ToolMetadata(
        name="read_sandbox_file",
        description="Read a UTF-8 text file from the artifact sandbox",
    ),
    ToolMetadata(
        name="verify_artifact",
        description="Verify a generated artifact and prepare its preview before saving",
    ),
    ToolMetadata(
        name="generate_image",
        description="Generate images from text descriptions using AI image models",
    ),
    ToolMetadata(
        name="search_knowledge_base",
        description="Search the user's knowledge base with hybrid semantic + keyword retrieval",
    ),
    ToolMetadata(
        name="create_automation",
        description="Draft an automation from an NL intent; user approves the card; tool saves",
    ),
    ToolMetadata(
        name="update_memory",
        description="Save important long-term facts, preferences, and instructions to the (personal or team) memory",
    ),
    ToolMetadata(
        name="create_google_drive_file",
        description="Create a new Google Doc or Google Sheet in Google Drive",
    ),
    ToolMetadata(
        name="delete_google_drive_file",
        description="Move an indexed Google Drive file to trash",
    ),
]
