"""Build the per-invocation ``SurfSenseContextSchema`` for a resume turn.

Resume doesn't carry new ``mentioned_document_ids`` (those are seeded by the
original turn). We still build the context so future middleware extensions
can rely on ``runtime.context`` always being populated.
"""

from __future__ import annotations

from app.agents.chat.shared.context import SurfSenseContextSchema


def build_resume_chat_runtime_context(
    *,
    workspace_id: int,
    request_id: str | None,
    turn_id: str,
    document_scope: list[int] | None = None,
) -> SurfSenseContextSchema:
    return SurfSenseContextSchema(
        workspace_id=workspace_id,
        request_id=request_id,
        turn_id=turn_id,
        document_scope=None if document_scope is None else list(document_scope),
    )
