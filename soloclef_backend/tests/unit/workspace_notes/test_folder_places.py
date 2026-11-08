"""Where a note document sits in the documents tree, for the Notes page's groups."""

from __future__ import annotations

from typing import NamedTuple

import pytest

from app.workspace_notes.folder_places import places_from_folders

pytestmark = pytest.mark.unit


class Row(NamedTuple):
    id: int
    name: str
    parent_id: int | None


def test_each_folder_gets_its_path_from_the_top_and_its_place_in_the_tree():
    # Rows arrive as the tree orders siblings (by position): Projects before
    # notes at the top, and Projects' own children in their order.
    places = places_from_folders(
        [
            Row(10, "Projects", None),
            Row(1, "notes", None),
            Row(12, "2026", 10),
            Row(11, "Archive", 10),
            Row(13, "Q4", 12),
        ]
    )

    assert {fid: place.path for fid, place in places.items()} == {
        10: ["Projects"],
        12: ["Projects", "2026"],
        13: ["Projects", "2026", "Q4"],
        11: ["Projects", "Archive"],
        1: ["notes"],
    }
    # Depth first: a folder, then everything below it, then its next sibling.
    in_tree_order = sorted(places.values(), key=lambda place: place.tree_order)
    assert [place.id for place in in_tree_order] == [10, 12, 13, 11, 1]
    assert places[13].name == "Q4"


def test_a_folder_whose_parent_is_gone_or_loops_still_gets_a_place():
    places = places_from_folders(
        [
            Row(1, "notes", None),
            Row(2, "orphan", 99),
            Row(3, "loop-a", 4),
            Row(4, "loop-b", 3),
        ]
    )

    assert places[2].path == ["orphan"]
    assert places[3].path == ["loop-a"]
    assert places[4].path == ["loop-b"]
    assert sorted(place.tree_order for place in places.values()) == [0, 1, 2, 3]


def test_no_folders_no_places():
    assert places_from_folders([]) == {}
