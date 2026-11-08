"""Run one registered task body as a process, for the Go worker tier.

A Go worker starts this module per job: ``python -m app.tasks.run <task-name>``
with the delivery as JSON on stdin. It calls the same function a Celery worker
would have called, with the same per-process setup and the same request
context, and writes one JSON line on stdout saying what happened.

It deliberately does not touch the result backend. The Go worker owns the
``celery-task-meta-*`` rows, the time limits and the acknowledgement
(``soloclef_worker/internal/executor``); this module owns running the body and
reporting on it, and nothing else.
"""

from __future__ import annotations

import logging
import os
import signal
import sys
import traceback
from contextlib import contextmanager
from dataclasses import dataclass
from typing import Any

from celery.exceptions import NotRegistered, Retry, SoftTimeLimitExceeded
from celery.signals import (
    setup_logging,
    task_postrun,
    task_prerun,
    worker_process_init,
    worker_process_shutdown,
)
from kombu.utils.json import dumps as encode_json, loads as decode_json

from app.celery_app import celery_app

logger = logging.getLogger(__name__)

#: The statuses a report may carry. Anything else the Go worker treats as no
#: report at all, so these three are a contract rather than a convention.
SUCCESS = "SUCCESS"
FAILURE = "FAILURE"
RETRY = "RETRY"


@dataclass(frozen=True)
class TaskCall:
    """One delivery, as the Go worker hands it over.

    The task name is not here: it is the command-line argument, so the two
    cannot disagree about which body is running.
    """

    id: str
    args: list[Any]
    kwargs: dict[str, Any]
    retries: int
    queue: str
    exchange: str
    root_id: str | None
    parent_id: str | None
    redelivered: bool
    worker: str
    soft_time_limit: float | None
    hard_time_limit: float | None


def task_name_from_argv(argv: list[str]) -> str:
    """The one task this process runs."""
    if len(argv) != 2 or not argv[1].strip():
        raise ValueError("usage: python -m app.tasks.run <task-name>")
    return argv[1].strip()


def read_call(raw: bytes) -> TaskCall:
    """Decode the delivery.

    kombu's decoder, not json's: Celery encodes a task's arguments with it, so
    a UUID or a datetime argument has to come back as a UUID or a datetime
    here exactly as it would inside a Celery worker.
    """
    try:
        payload = decode_json(raw.decode("utf-8"))
    except Exception as exc:
        raise ValueError(f"the call is not JSON: {exc}") from exc
    if not isinstance(payload, dict):
        raise ValueError("the call must be a JSON object")
    return call_from(payload)


def call_from(payload: dict[str, Any]) -> TaskCall:
    """The delivery, from an already-decoded object.

    Separate from read_call because the warm process (app/tasks/serve.py) reads
    a line that carries the call inside it, and both shapes have to agree on
    what a delivery is.
    """
    identifier = str(payload.get("id") or "").strip()
    if not identifier:
        raise ValueError("the call names no task id, so no row could be written for it")
    args = payload.get("args", [])
    kwargs = payload.get("kwargs", {})
    if not isinstance(args, list) or not isinstance(kwargs, dict):
        raise ValueError("args must be a list and kwargs an object")

    return TaskCall(
        id=identifier,
        args=args,
        kwargs=kwargs,
        retries=int(payload.get("retries") or 0),
        queue=str(payload.get("queue") or ""),
        exchange=str(payload.get("exchange") or ""),
        root_id=payload.get("root_id") or None,
        parent_id=payload.get("parent_id") or None,
        redelivered=bool(payload.get("redelivered")),
        worker=str(payload.get("worker") or ""),
        soft_time_limit=payload.get("soft_time_limit") or None,
        hard_time_limit=payload.get("hard_time_limit") or None,
    )


def run_task(name: str, call: TaskCall, *, app=celery_app) -> dict[str, Any]:
    """Call the registered body and say what happened.

    ``Task.run`` rather than the task itself: it is the function the decorator
    wrapped, so a bound task still receives ``self``, while none of Celery's
    worker machinery — tracer, result backend, acknowledgement — is involved.
    """
    task = app.tasks.get(name)
    if task is None:
        return failure_report(NotRegistered(name))

    task.push_request(**request_fields(name, call))
    try:
        with task_signals(task, call):
            value = task.run(*call.args, **call.kwargs)
    except Retry as retry:
        return retry_report(retry)
    except Exception as exc:
        return failure_report(exc)
    finally:
        task.pop_request()
    return success_report(value)


def request_fields(name: str, call: TaskCall) -> dict[str, Any]:
    """The request a bound task reads back as ``self.request``.

    ``called_directly`` is False because it decides what ``self.retry`` does:
    left True it re-raises the error and the retry is lost, while False makes
    Celery publish the next attempt — which the knowledge-store index and the
    deliverable job tasks rely on.
    """
    return {
        "id": call.id,
        "task": name,
        "args": call.args,
        "kwargs": call.kwargs,
        "retries": call.retries,
        "root_id": call.root_id,
        "parent_id": call.parent_id,
        "hostname": call.worker,
        "correlation_id": call.id,
        "called_directly": False,
        "is_eager": False,
        "delivery_info": {
            "exchange": call.exchange,
            "routing_key": call.queue,
            "redelivered": call.redelivered,
        },
        "timelimit": [call.soft_time_limit, call.hard_time_limit],
    }


@contextmanager
def task_signals(task, call: TaskCall):
    """Wrap the body in the signals this app hangs its own work on.

    ``task_prerun``/``task_postrun`` carry the log context (the task name and
    a short id on every line the body logs) and the Celery span attributes;
    see app/observability/logs/celery_signals.py.
    """
    task_prerun.send(
        sender=task, task_id=call.id, task=task, args=call.args, kwargs=call.kwargs
    )
    try:
        yield
    finally:
        task_postrun.send(
            sender=task, task_id=call.id, task=task, args=call.args, kwargs=call.kwargs
        )


def success_report(value: Any) -> dict[str, Any]:
    """A value the body returned, if it can be stored."""
    try:
        encode_json(value)
    except Exception as exc:
        # Celery fails the same way: a return value the result serializer
        # cannot encode is a failed task, however well the body ran.
        from kombu.exceptions import EncodeError

        return failure_report(EncodeError(str(exc)))
    return {"status": SUCCESS, "result": value}


def failure_report(exc: BaseException) -> dict[str, Any]:
    """A failure in the shape Celery's result backend stores one."""
    return {
        "status": FAILURE,
        "exc_type": type(exc).__qualname__,
        "exc_module": type(exc).__module__,
        "exc_message": storable_args(exc),
        "traceback": current_traceback(),
    }


def retry_report(retry: Retry) -> dict[str, Any]:
    """A body that asked to be retried.

    Asking is publishing: ``Task.retry`` sends the next attempt before raising,
    so this delivery is finished and the row says RETRY, as Celery's does.
    """
    report = failure_report(retry.exc if retry.exc is not None else retry)
    report["status"] = RETRY
    return report


def storable_args(exc: BaseException) -> list[Any]:
    """The exception's own arguments, with whatever will not encode repr'd."""
    stored: list[Any] = []
    for arg in getattr(exc, "args", ()) or ():
        try:
            encode_json(arg)
        except Exception:
            arg = repr(arg)
        stored.append(arg)
    return stored


def current_traceback() -> str:
    """The traceback being handled, or nothing when there is none."""
    if sys.exc_info()[0] is None:
        return ""
    return traceback.format_exc()


def report_line(report: dict[str, Any]) -> bytes:
    """The report as the one line the Go worker reads on stdout."""
    return encode_json(report).encode("utf-8") + b"\n"


def write_line(fd: int, line: bytes) -> None:
    """Write a whole line, however many writes that takes.

    os.write is free to write part of a buffer, and half a report is a report
    the worker cannot read — so a large return value would be the one that
    broke.
    """
    while line:
        line = line[os.write(fd, line) :]


def take_over_stdout() -> int:
    """Keep stdout for the report, and send everything else to the logs.

    A task body here starts ffmpeg and other tools that write to file
    descriptor 1 directly. Pointing descriptor 1 at stderr, after keeping a
    duplicate for the report, means anything printed by this process or
    anything it starts lands in the worker's logs rather than in the middle of
    the report.
    """
    sys.stdout.flush()
    report_fd = os.dup(1)
    os.dup2(2, 1)
    return report_fd


def raise_soft_limit_on_signal(limit: float | None) -> None:
    """Turn the worker's SIGUSR1 into SoftTimeLimitExceeded inside the body.

    The same signal Celery's prefork pool uses for the soft limit, so a body
    that catches it — to release a billing hold or mark a row failed — behaves
    here exactly as it does under a Celery worker.
    """

    def raise_soft_limit(_signum, _frame):
        raise SoftTimeLimitExceeded(limit)

    signal.signal(signal.SIGUSR1, raise_soft_limit)


def prepare_process() -> None:
    """Give the body the process a Celery worker's child would have given it.

    This app's own hooks, in the order a worker fires them: the log format and
    context, then the LiteLLM routers, the pricing registration and OTel
    (``init_worker`` in app/celery_app.py). ``worker_ready`` is deliberately
    left alone — it is per worker, not per task, and its startup lines would
    otherwise be logged once per job.
    """
    setup_logging.send(sender=None)
    worker_process_init.send(sender=None)


def finish_process() -> None:
    """Flush what the process holds, as a worker's child does on its way out."""
    try:
        worker_process_shutdown.send(sender=None)
    except Exception:
        logger.warning("the task process did not shut down cleanly", exc_info=True)


def import_task_modules(app=celery_app) -> None:
    """Fill the task registry the way a worker fills it.

    ``include=`` in app/celery_app.py is lazy: importing the Celery app
    registers nothing by itself, so the modules are imported through the app's
    own loader rather than guessed at from the task name.
    """
    app.loader.import_default_modules()


def main(argv: list[str] | None = None) -> int:
    """Run one task body; 0 only if it succeeded."""
    report_fd = take_over_stdout()
    try:
        report = _execute(sys.argv if argv is None else argv, sys.stdin.buffer.read())
    except Exception as exc:
        # The report is this process's only output, so even a failure to set
        # up has to arrive as one.
        report = failure_report(exc)
    write_line(report_fd, report_line(report))
    return 0 if report["status"] == SUCCESS else 1


def _execute(argv: list[str], stdin: bytes) -> dict[str, Any]:
    name = task_name_from_argv(argv)
    call = read_call(stdin)
    prepare_process()
    raise_soft_limit_on_signal(call.soft_time_limit)
    try:
        import_task_modules()
        return run_task(name, call)
    finally:
        finish_process()


if __name__ == "__main__":
    sys.exit(main())
