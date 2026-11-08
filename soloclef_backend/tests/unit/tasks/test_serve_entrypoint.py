"""The warm half of the contract between the Go worker tier and this app.

``app.tasks.serve`` is the process a Go worker keeps: this app is imported
once, the process says it is ready, and then it runs body after body, one at a
time, taking each job as a line on stdin and answering with a line on stdout.
The report inside that line is ``app.tasks.run``'s report unchanged, because
the result row a client reads must not depend on which shape ran the body.

These tests pin the loop itself — a report per job, carrying that job's id, and
the soft limit armed only while a body is running.
"""

from __future__ import annotations

import os
import signal

import pytest
from kombu.utils.json import dumps as encode_json, loads as decode_json

from app.celery_app import celery_app
from app.tasks.run import TaskCall
from app.tasks.serve import read_request, ready_line, serve

pytestmark = pytest.mark.unit


@celery_app.task(name="tests.serve_entrypoint.plain")
def _plain(number):
    return {"doubled": number * 2}


@celery_app.task(name="tests.serve_entrypoint.raises")
def _raises():
    raise ValueError("boom")


@celery_app.task(name="tests.serve_entrypoint.soft_limit")
def _soft_limit():
    from celery.exceptions import SoftTimeLimitExceeded

    try:
        os.kill(os.getpid(), signal.SIGUSR1)
    except SoftTimeLimitExceeded as exc:
        return {"caught": str(exc)}
    return {"caught": None}


def request_line(task: str, task_id: str, args: list | None = None) -> bytes:
    call = {
        "id": task_id,
        "args": args or [],
        "kwargs": {},
        "retries": 0,
        "queue": "surfsense.heavy",
        "exchange": "surfsense",
        "root_id": task_id,
        "parent_id": None,
        "redelivered": False,
        "worker": "go-worker@test",
        "soft_time_limit": 30.0,
        "hard_time_limit": 60.0,
    }
    return encode_json({"task": task, "call": call}).encode("utf-8") + b"\n"


def reports_of(written: list[bytes]) -> list[dict]:
    return [decode_json(line.decode("utf-8")) for line in written]


def test_every_job_gets_a_report_carrying_its_own_id():
    written: list[bytes] = []
    serve(
        iter(
            [
                request_line("tests.serve_entrypoint.plain", "job-1", [2]),
                request_line("tests.serve_entrypoint.plain", "job-2", [3]),
            ]
        ),
        written.append,
    )

    answers = reports_of(written)
    assert [answer["id"] for answer in answers] == ["job-1", "job-2"]
    assert [answer["report"]["result"] for answer in answers] == [
        {"doubled": 4},
        {"doubled": 6},
    ]
    assert all(answer["report"]["status"] == "SUCCESS" for answer in answers)


def test_a_body_that_raises_is_reported_as_the_one_shot_entrypoint_reports_it():
    written: list[bytes] = []
    serve(
        iter([request_line("tests.serve_entrypoint.raises", "job-1")]), written.append
    )

    report = reports_of(written)[0]["report"]
    assert report["status"] == "FAILURE"
    assert report["exc_type"] == "ValueError"
    assert report["exc_module"] == "builtins"
    assert report["exc_message"] == ["boom"]
    assert "ValueError" in report["traceback"]


def test_a_request_this_process_cannot_read_ends_it():
    """A request with no job in it names nothing to report on, and the worker
    is holding a job it will never hear about. Ending is what the worker
    notices at once — the job's row says the worker was lost and the process is
    replaced — while carrying on would leave that job STARTED until its hard
    time limit, hours later."""
    written: list[bytes] = []
    with pytest.raises(ValueError):
        serve(
            iter(
                [
                    encode_json({"task": "", "call": {}}).encode("utf-8"),
                    request_line("tests.serve_entrypoint.plain", "job-2", [1]),
                ]
            ),
            written.append,
        )

    assert written == [], "nothing can be reported about a request with no job in it"


def test_the_soft_limit_is_armed_for_a_body_and_disarmed_between_jobs():
    signal.signal(signal.SIGUSR1, signal.SIG_IGN)
    written: list[bytes] = []
    serve(
        iter([request_line("tests.serve_entrypoint.soft_limit", "job-1")]),
        written.append,
    )

    report = reports_of(written)[0]["report"]
    assert report["status"] == "SUCCESS"
    assert "30.0" in (report["result"]["caught"] or ""), (
        "the body must see the worker's SIGUSR1 as SoftTimeLimitExceeded, "
        f"got {report['result']}"
    )
    assert signal.getsignal(signal.SIGUSR1) is signal.SIG_IGN, (
        "a signal arriving between jobs must not raise into the loop"
    )


def test_the_ready_line_is_what_tells_the_worker_the_imports_are_paid():
    ready = decode_json(ready_line().decode("utf-8"))
    assert ready["ready"] is True
    assert ready["pid"] == os.getpid()


def test_a_request_is_a_task_name_and_a_delivery():
    name, call = read_request(
        request_line("tests.serve_entrypoint.plain", "job-1", [7])
    )
    assert name == "tests.serve_entrypoint.plain"
    assert isinstance(call, TaskCall)
    assert call.id == "job-1"
    assert call.args == [7]
    assert call.soft_time_limit == 30.0


@pytest.mark.parametrize(
    "payload",
    [
        b"not json at all",
        encode_json(["a", "list"]).encode("utf-8"),
        encode_json({"call": {"id": "job-1"}}).encode("utf-8"),
        encode_json({"task": "tests.serve_entrypoint.plain"}).encode("utf-8"),
    ],
)
def test_a_request_that_is_not_a_job_is_refused_rather_than_guessed_at(payload):
    with pytest.raises(ValueError):
        read_request(payload)
