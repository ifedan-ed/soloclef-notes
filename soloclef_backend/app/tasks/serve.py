"""Run task bodies in one warm process, for the Go worker tier.

A Go worker starts this module once per warm process: ``python -m
app.tasks.serve``. It imports this app, says it is ready, and then runs body
after body — one at a time — taking each job as a JSON line on stdin and
answering with a JSON line on stdout.

The reason it exists is the cost of the alternative: starting a process per job
means importing this app per job, measured at 27-33 seconds before the body
starts. The body, the request context, the signals and the report are
``app.tasks.run``'s; this module only keeps the process alive between them, so
a row a client reads cannot depend on which shape ran the task.

It owns no more than that. The result rows, the time limits, the
acknowledgement and the decision to replace a worn-out process are the Go
worker's (``soloclef_worker/internal/executor``).
"""

from __future__ import annotations

import logging
import os
import signal
import sys
from collections.abc import Callable, Iterable, Iterator
from contextlib import contextmanager
from typing import Any

from kombu.utils.json import dumps as encode_json, loads as decode_json

from app.celery_app import celery_app
from app.tasks.run import (
    TaskCall,
    call_from,
    finish_process,
    import_task_modules,
    prepare_process,
    raise_soft_limit_on_signal,
    run_task,
    take_over_stdout,
    write_line,
)

logger = logging.getLogger(__name__)


def read_request(raw: bytes) -> tuple[str, TaskCall]:
    """Decode one job: which body to run, and the delivery to run it with.

    The task name is in the line rather than on the command line, because a
    warm process runs whatever job comes next.
    """
    try:
        payload = decode_json(raw.decode("utf-8"))
    except Exception as exc:
        raise ValueError(f"the request is not JSON: {exc}") from exc
    if not isinstance(payload, dict):
        raise ValueError("the request must be a JSON object")

    name = str(payload.get("task") or "").strip()
    if not name:
        raise ValueError("the request names no task to run")
    call = payload.get("call")
    if not isinstance(call, dict):
        raise ValueError("the request carries no call to run the task with")
    return name, call_from(call)


def ready_line() -> bytes:
    """What tells the worker this process has paid the imports.

    Until it arrives the worker has a process it may not give work to, which is
    also how a command that is not this module is caught: it never says it.
    """
    return encode_json({"ready": True, "pid": os.getpid()}).encode("utf-8") + b"\n"


def response_line(task_id: str, report: dict[str, Any]) -> bytes:
    """One job's report, named for the job it belongs to.

    The id is the worker's guard: a report for any other job means this process
    is out of step with its work, and the worker retires it rather than
    attributing the next report to the wrong task.
    """
    return encode_json({"id": task_id, "report": report}).encode("utf-8") + b"\n"


@contextmanager
def soft_limit_armed(limit: float | None) -> Iterator[None]:
    """Turn the worker's SIGUSR1 into SoftTimeLimitExceeded, for this job only.

    Armed around the body and ignored between jobs: the signal is the worker's
    way of asking a body to stop, and one that arrived while this process was
    waiting for its next job would otherwise raise into the loop that reads it.
    """
    raise_soft_limit_on_signal(limit)
    try:
        yield
    finally:
        signal.signal(signal.SIGUSR1, signal.SIG_IGN)


def serve(
    requests: Iterable[bytes],
    write: Callable[[bytes], None],
    *,
    app=celery_app,
) -> None:
    """Run the jobs as they arrive, one at a time, until there are no more.

    One job at a time on purpose: concurrency is the Go worker's, which gives a
    job a process rather than a thread, so a body here never shares an
    interpreter with another running body — as a Celery prefork child never
    does.

    A request this process cannot read ends it. There is no job to report on
    and the worker is holding one it will never hear about: ending is what the
    worker notices at once, while carrying on would leave that job STARTED
    until its hard time limit.
    """
    for raw in requests:
        if not raw.strip():
            continue
        name, call = read_request(raw)
        with soft_limit_armed(call.soft_time_limit):
            report = run_task(name, call, app=app)
        write(response_line(call.id, report))


def main() -> int:
    """Be the warm process: import once, then serve until stdin ends."""
    report_fd = take_over_stdout()

    def write(line: bytes) -> None:
        write_line(report_fd, line)

    try:
        prepare_process()
        import_task_modules()
    except Exception:
        # The worker is waiting for a ready line it is never going to get. The
        # logs are the only place that can say why, and ending is what the
        # worker is watching for.
        logger.exception("a warm task process could not import this app")
        return 1

    signal.signal(signal.SIGUSR1, signal.SIG_IGN)
    write(ready_line())

    try:
        serve(sys.stdin.buffer, write)
    except Exception:
        logger.exception("a warm task process stopped serving")
        return 1
    finally:
        finish_process()
    return 0


if __name__ == "__main__":
    sys.exit(main())
