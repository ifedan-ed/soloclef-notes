"""The contract between the Go worker tier and this app's task bodies.

``app.tasks.run`` is the process a Go worker starts per job: the delivery
arrives as JSON on stdin, the body Celery would have called runs, and what
happened goes back as JSON on stdout. These tests pin the seam itself — the
request a bound task sees, the shape of each report, and the encoding — because
the Go side turns those reports straight into Celery result rows
(``soloclef_worker/internal/executor``).
"""

from __future__ import annotations

import os
import signal
import uuid

import pytest
from celery.exceptions import SoftTimeLimitExceeded

from app.celery_app import celery_app
from app.tasks.run import (
    TaskCall,
    raise_soft_limit_on_signal,
    read_call,
    report_line,
    run_task,
    task_name_from_argv,
)

pytestmark = pytest.mark.unit


@celery_app.task(name="tests.run_entrypoint.plain")
def _plain(first, second=None):
    return {"first": first, "second": second}


@celery_app.task(name="tests.run_entrypoint.bound", bind=True)
def _bound(self):
    return {
        "id": self.request.id,
        "retries": self.request.retries,
        "hostname": self.request.hostname,
        "routing_key": (self.request.delivery_info or {}).get("routing_key"),
        "called_directly": self.request.called_directly,
    }


@celery_app.task(name="tests.run_entrypoint.raises")
def _raises():
    raise ValueError("boom")


@celery_app.task(name="tests.run_entrypoint.unencodable")
def _unencodable():
    return object()


@celery_app.task(name="tests.run_entrypoint.soft_limit")
def _soft_limit():
    os.kill(os.getpid(), signal.SIGUSR1)
    # The signal is delivered between bytecodes, so the raise lands here.
    for _ in range(1000):
        pass
    return "the soft limit never arrived"


@celery_app.task(name="tests.run_entrypoint.retrying", bind=True)
def _retrying(self):
    raise self.retry(countdown=30, exc=RuntimeError("locked"))


def _call(**overrides) -> TaskCall:
    fields = {
        "id": "a-task-id",
        "args": [],
        "kwargs": {},
        "retries": 0,
        "queue": "surfsense.heavy",
        "exchange": "",
        "root_id": "a-task-id",
        "parent_id": None,
        "redelivered": False,
        "worker": "go-worker@host",
        "soft_time_limit": 600.0,
        "hard_time_limit": 900.0,
    }
    fields.update(overrides)
    return TaskCall(**fields)


def test_a_body_runs_with_the_arguments_it_was_sent():
    report = run_task(
        "tests.run_entrypoint.plain",
        _call(args=[1], kwargs={"second": "two"}),
    )

    assert report["status"] == "SUCCESS"
    assert report["result"] == {"first": 1, "second": "two"}


def test_a_bound_task_sees_the_delivery_as_its_own_request():
    report = run_task("tests.run_entrypoint.bound", _call(retries=3))

    assert report["status"] == "SUCCESS"
    assert report["result"] == {
        "id": "a-task-id",
        "retries": 3,
        "hostname": "go-worker@host",
        "routing_key": "surfsense.heavy",
        # False, or self.retry would re-raise instead of queueing the retry.
        "called_directly": False,
    }


def test_a_failing_body_is_reported_as_the_exception_it_raised():
    report = run_task("tests.run_entrypoint.raises", _call())

    assert report["status"] == "FAILURE"
    assert report["exc_type"] == "ValueError"
    assert report["exc_module"] == "builtins"
    assert report["exc_message"] == ["boom"]
    assert "ValueError" in report["traceback"]


def test_a_task_nobody_registered_is_reported_not_registered():
    report = run_task("tests.run_entrypoint.no_such_task", _call())

    assert report["status"] == "FAILURE"
    assert report["exc_type"] == "NotRegistered"


def test_a_result_that_cannot_be_encoded_is_reported_not_raised():
    report = run_task("tests.run_entrypoint.unencodable", _call())

    assert report["status"] == "FAILURE"
    assert report["exc_type"] == "EncodeError"


def test_the_soft_limit_signal_is_raised_inside_the_body():
    raise_soft_limit_on_signal(600.0)
    try:
        report = run_task("tests.run_entrypoint.soft_limit", _call())
    finally:
        signal.signal(signal.SIGUSR1, signal.SIG_DFL)

    assert report["status"] == "FAILURE"
    assert report["exc_type"] == SoftTimeLimitExceeded.__name__


def test_a_retry_republishes_the_task_and_is_reported_as_a_retry(monkeypatch):
    published = []
    monkeypatch.setattr(
        "celery.canvas.Signature.apply_async",
        lambda self, *a, **kw: published.append(self),
    )

    report = run_task("tests.run_entrypoint.retrying", _call(retries=1))

    assert report["status"] == "RETRY"
    assert report["exc_type"] == "RuntimeError"
    # The body asked Celery to retry, so the next attempt is already queued:
    # the Go worker acknowledges this delivery instead of putting it back.
    assert len(published) == 1
    assert published[0].options["retries"] == 2


def test_the_call_is_decoded_the_way_celery_decodes_a_body():
    identifier = uuid.uuid4()
    raw = (
        '{"id": "x", "args": [{"__type__": "uuid", "__value__": {"hex": "'
        + identifier.hex
        + '"}}], "kwargs": {}, "retries": 0, "queue": "surfsense",'
        ' "exchange": "", "root_id": "x", "parent_id": null,'
        ' "redelivered": false, "worker": "go-worker@host",'
        ' "soft_time_limit": 1.0, "hard_time_limit": 2.0}'
    )

    call = read_call(raw.encode())

    # kombu's encoder, not json's: a UUID argument must reach the body as a
    # UUID, exactly as it would from a Celery worker.
    assert call.args == [identifier]
    assert call.queue == "surfsense"
    assert call.soft_time_limit == 1.0


def test_a_call_that_is_not_a_delivery_is_refused():
    for raw in [b"", b"{", b'{"id": ""}', b'{"id": "x", "args": {}}']:
        with pytest.raises(ValueError):
            read_call(raw)


def test_the_task_name_comes_from_the_command_line():
    assert task_name_from_argv(["app.tasks.run", "podcast.render_audio"]) == (
        "podcast.render_audio"
    )
    for argv in [["app.tasks.run"], ["app.tasks.run", "a", "b"]]:
        with pytest.raises(ValueError):
            task_name_from_argv(argv)


def test_the_report_is_one_line_the_go_worker_can_read():
    line = report_line({"status": "FAILURE", "traceback": "line one\nline two"})

    assert line.endswith(b"\n")
    assert line.count(b"\n") == 1
