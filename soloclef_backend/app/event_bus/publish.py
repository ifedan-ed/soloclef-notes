"""State a domain fact. The one call a producer makes, in any process.

Stamps the :class:`Event` and hands it to the stream, so the subscribers run
wherever a dispatcher runs rather than wherever the producer did — which is what
lets a producer be something other than this interpreter.

Call it after your commit. An event is a fact, and a transaction that rolls back
has no fact to report.
"""

from __future__ import annotations

from typing import Any

from .event import Event
from .stream.publisher import add_with_own_client


async def publish(
    event_type: str,
    payload: dict[str, Any] | None = None,
    *,
    workspace_id: int,
) -> Event:
    """Publish ``event_type`` and return the stamped event.

    Raises whatever the stream raised: the caller decides whether a fact it
    could not state is worth failing for.
    """
    event = Event(
        event_type=event_type,
        payload=payload or {},
        workspace_id=workspace_id,
    )
    await add_with_own_client(event)
    return event
