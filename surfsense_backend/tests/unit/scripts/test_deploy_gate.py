"""The deploy gate holds while anything generates, and when it cannot tell."""

from __future__ import annotations

import json

import pytest

from scripts import deploy_gate

pytestmark = pytest.mark.unit

_IDLE_CELERY = 'Some startup log line\n{"celery@w1": []}\n'


def _runner(*, db=None, ps="", celery=_IDLE_CELERY, fail=()):
    """A fake ``docker`` that answers each probe from the given values."""
    db = db or {}

    def run(argv):
        joined = " ".join(argv)
        for key in fail:
            if key in joined:
                raise RuntimeError(f"{key} unreachable")
        if "psql" in joined:
            for table, count in db.items():
                if f"from {table} " in argv[-1]:
                    return f"{count}\n"
            return "0\n"
        if argv[:2] == ["docker", "ps"]:
            return ps
        if "inspect" in argv:
            return celery
        raise AssertionError(f"unexpected command {argv}")

    return run


def test_everything_idle_opens_the_gate(capsys):
    assert deploy_gate.main([], run=_runner()) == 0
    assert "CLEAR" in capsys.readouterr().out


@pytest.mark.parametrize(
    "table",
    [
        "deliverable_jobs",
        "chat_session_state",
        "podcast_runs",
        "video_presentation_runs",
    ],
)
def test_an_in_flight_row_holds_the_gate(table):
    assert deploy_gate.main([], run=_runner(db={table: 2})) == 1


def test_a_running_sandbox_holds_the_gate_but_other_containers_do_not():
    ps = (
        "soloclef-backend-fast:latest\tsoloclef-backend-1\n"
        "soloclef-sandbox-local:latest\tsandbox-abc\n"
    )
    probe = deploy_gate.sandbox_probe(_runner(ps=ps))
    assert (probe.busy, probe.detail) == (1, "sandbox-abc")
    assert deploy_gate.main([], run=_runner(ps=ps)) == 1
    other = "soloclef-backend-fast:latest\tsoloclef-backend-1\n"
    assert deploy_gate.sandbox_probe(_runner(ps=other)).busy == 0


def test_only_generation_tasks_count_as_busy_celery():
    reply = {
        "celery@w1": [
            {"name": "deliverables.execute_queued"},
            {"name": "podcast.render_audio"},
            {"name": "index_notion_pages"},
        ]
    }
    out = "banner line\n" + json.dumps(reply)
    assert deploy_gate.active_generation_tasks(out) == [
        "deliverables.execute_queued",
        "podcast.render_audio",
    ]
    assert deploy_gate.main([], run=_runner(celery=out)) == 1
    indexing_only = json.dumps({"celery@w1": [{"name": "index_notion_pages"}]})
    assert deploy_gate.main([], run=_runner(celery=indexing_only)) == 0


@pytest.mark.parametrize("unreachable", ["psql", "inspect", "ps --filter"])
def test_a_probe_that_cannot_answer_holds_the_gate(unreachable, capsys):
    assert deploy_gate.main([], run=_runner(fail=[unreachable])) == 1
    assert "=?" in capsys.readouterr().out


def test_unparseable_celery_reply_holds_the_gate():
    probe = deploy_gate.celery_probe(_runner(celery="Error: no nodes replied\n"))
    assert probe.busy is None


def test_wait_polls_until_clear():
    states = iter([{"deliverable_jobs": 1}, {"deliverable_jobs": 1}, {}])
    current = {}

    def run(argv):
        return _runner(db=current)(argv)

    sleeps = []

    def sleep(seconds):
        sleeps.append(seconds)
        current.clear()
        current.update(next(states))

    current.update(next(states))
    assert deploy_gate.main(["--wait", "--interval", "30"], run=run, sleep=sleep) == 0
    assert sleeps == [30, 30]


def test_wait_gives_up_at_the_timeout():
    now = [0.0]

    def sleep(seconds):
        now[0] += seconds

    code = deploy_gate.main(
        ["--wait", "--interval", "30", "--timeout", "90"],
        run=_runner(db={"deliverable_jobs": 1}),
        sleep=sleep,
        clock=lambda: now[0],
    )
    assert code == 1
    assert now[0] == 90
