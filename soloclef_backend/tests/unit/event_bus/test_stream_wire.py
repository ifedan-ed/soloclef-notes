"""The stream entry shape — the contract a non-Python publisher has to match."""

from __future__ import annotations

import json

import pytest

from app.event_bus.event import Event
from app.event_bus.stream.wire import FIELD, decode, encode

pytestmark = pytest.mark.unit


def _event() -> Event:
    return Event(
        event_type="document.entered_folder",
        payload={"document_id": 42, "folder_id": 7},
        workspace_id=3,
    )


def test_an_event_becomes_one_field_holding_its_json() -> None:
    fields = encode(_event())

    assert list(fields) == [FIELD]
    assert json.loads(fields[FIELD])["event_type"] == "document.entered_folder"


def test_encode_then_decode_reproduces_the_event() -> None:
    event = _event()

    assert decode(encode(event)) == event


def test_an_entry_written_elsewhere_decodes_to_the_same_event() -> None:
    """Another language stamps the time its own way; the event must be equal."""
    event = _event()
    elsewhere = {
        FIELD: json.dumps(
            {
                "event_type": event.event_type,
                "payload": event.payload,
                "workspace_id": event.workspace_id,
                "event_id": event.event_id,
                "occurred_at": event.occurred_at.isoformat(),
            }
        )
    }

    assert decode(elsewhere) == event


def test_an_entry_that_is_not_an_event_is_refused() -> None:
    with pytest.raises(ValueError):
        decode({FIELD: json.dumps({"event_type": "x.happened"})})


def test_an_entry_without_the_field_is_refused() -> None:
    with pytest.raises(KeyError):
        decode({"something_else": "{}"})
