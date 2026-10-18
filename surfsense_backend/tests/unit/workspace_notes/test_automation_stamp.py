"""The stamp an automation run puts on the note files its own agent writes."""

from __future__ import annotations

import argparse

import pytest

from app.workspace_notes import automation_stamp
from app.workspace_notes.automation_stamp import (
    CONFIG_KEY,
    current_stamp,
    note_stamp,
    stamp_of,
)
from scripts.attach_automation_notes import document_ids

pytestmark = pytest.mark.unit


def test_a_run_stamp_names_the_automation_and_the_run():
    assert note_stamp(automation_id=3, automation_name="Daily", run_id=14) == {
        "automation_id": 3,
        "automation_name": "Daily",
        "automation_run_id": 14,
    }
    assert "automation_run_id" not in note_stamp(
        automation_id=3, automation_name="Daily", run_id=None
    )


def test_only_a_config_carrying_a_run_stamp_has_one():
    stamp = note_stamp(automation_id=3, automation_name="Daily", run_id=14)
    assert stamp_of({"configurable": {"thread_id": "t", CONFIG_KEY: stamp}}) == stamp
    # A chat turn's config, or nothing at all.
    assert stamp_of({"configurable": {"thread_id": 7}}) is None
    assert stamp_of(None) is None
    # Malformed values never stamp anything.
    assert stamp_of({"configurable": {CONFIG_KEY: "3"}}) is None
    assert stamp_of({"configurable": {CONFIG_KEY: {"automation_id": True}}}) is None


def test_the_stamp_is_read_from_the_running_graphs_config(monkeypatch):
    stamp = note_stamp(automation_id=5, automation_name="Mail", run_id=None)
    monkeypatch.setattr(
        "langgraph.config.get_config",
        lambda: {"configurable": {CONFIG_KEY: stamp}},
    )
    assert current_stamp() == stamp


def test_outside_a_graph_there_is_no_stamp(monkeypatch):
    def outside():
        raise RuntimeError("Called get_config outside of a runnable context")

    monkeypatch.setattr("langgraph.config.get_config", outside)
    assert current_stamp() is None
    assert automation_stamp.CONFIG_KEY == CONFIG_KEY


@pytest.mark.parametrize(
    ("value", "ids"),
    [
        ("199", [199]),
        ("199,201", [199, 201]),
        ("199-202", [199, 200, 201, 202]),
        (" 198 , 205-206 ", [198, 205, 206]),
    ],
)
def test_the_script_takes_ids_lists_and_ranges(value, ids):
    assert document_ids(value) == ids


@pytest.mark.parametrize("value", ["abc", "207-199", "0", "1-x"])
def test_the_script_refuses_what_is_not_a_document_id(value):
    with pytest.raises(argparse.ArgumentTypeError):
        document_ids(value)
