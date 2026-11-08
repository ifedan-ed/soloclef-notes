"""Studio output titles follow their notebook's name."""

from types import SimpleNamespace

from app.notebooks.output_titles import (
    current_output_title,
    renamed_output_title,
    retitle_outputs,
)


def _notebook(title: str):
    return SimpleNamespace(title=title)


def test_a_stale_untitled_suffix_takes_the_current_name():
    named = _notebook("IQ, Motivation and Academic Performance")
    assert (
        current_output_title("Slide Deck: Untitled notebook", named)
        == "Slide Deck: IQ, Motivation and Academic Performance"
    )
    assert (
        current_output_title("Quiz: Cuaderno sin título", named)
        == "Quiz: IQ, Motivation and Academic Performance"
    )


def test_other_titles_are_kept():
    named = _notebook("Asthma")
    assert current_output_title("My own deck", named) == "My own deck"
    assert current_output_title("Quiz: Asthma", named) == "Quiz: Asthma"
    untitled = _notebook("Untitled notebook")
    assert (
        current_output_title("Quiz: Untitled notebook", untitled)
        == "Quiz: Untitled notebook"
    )


def test_renaming_only_touches_outputs_named_after_the_old_title():
    assert renamed_output_title("Mind Map: Old", "Old", "New") == "Mind Map: New"
    assert renamed_output_title("Mind Map: Older", "Old", "New") is None
    assert renamed_output_title("Renamed by me", "Old", "New") is None
    assert renamed_output_title(": Old", "Old", "New") is None


class _Session:
    def __init__(self, outputs):
        self.outputs = outputs
        self.executed = []

    async def scalars(self, _query):
        return iter(self.outputs)

    async def execute(self, _statement, params):
        self.executed.append(params)


async def test_retitle_outputs_renames_matching_outputs_only():
    outputs = [
        SimpleNamespace(title="Quiz: Untitled notebook", deliverable_job_id=7),
        SimpleNamespace(title="Slide Deck: Untitled notebook", deliverable_job_id=None),
        SimpleNamespace(title="Deck I renamed", deliverable_job_id=8),
        SimpleNamespace(title="Report: Something else", deliverable_job_id=9),
    ]
    session = _Session(outputs)
    changed = await retitle_outputs(
        session,
        11,
        {"Untitled notebook"},
        "Complications of Premature Delivery",
    )
    assert changed == 2
    assert [o.title for o in outputs] == [
        "Quiz: Complications of Premature Delivery",
        "Slide Deck: Complications of Premature Delivery",
        "Deck I renamed",
        "Report: Something else",
    ]
    # Only the retitled output with a saved document renames that document,
    # and only from the output's previous title.
    assert session.executed == [
        {
            "job": 7,
            "old": "Quiz: Untitled notebook",
            "new": "Quiz: Complications of Premature Delivery",
        }
    ]
    assert await retitle_outputs(_Session(outputs), 11, {"Same"}, "Same") == 0
