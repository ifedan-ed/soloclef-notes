"""Domain event bus.

Domain-agnostic pub/sub across processes. Producers ``await publish(...)``, which
puts the event on a Redis Stream; one dispatcher reads that stream and fans each
event out over the local ``bus``, which is where subscribers register. Domain
modules depend on it, never the reverse.

    from app.event_bus.publish import publish
    await publish("document.indexed", {"document_id": 42}, workspace_id=7)

    from app.event_bus import bus
    bus.subscribe(my_handler)
"""

from __future__ import annotations

from . import events  # noqa: F401  — populates the event-type catalog
from .bus import EventBus, Subscriber, bus
from .catalog import EventCatalog, EventType, catalog
from .event import Event

__all__ = [
    "Event",
    "EventBus",
    "EventCatalog",
    "EventType",
    "Subscriber",
    "bus",
    "catalog",
]
