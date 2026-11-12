"""Warm parser processes, one file each at a time.

Why processes and not threads: MuPDF is not thread-safe, a parse holds the
GIL for most of its run, and a file that crashes MuPDF's C code or sends it
into a loop must cost one request, not the server. A worker that dies or runs
past the time limit is killed and replaced, and only the request it was
serving is refused.

Why they are kept warm: importing pymupdf4llm and its layout model takes
about two seconds, more than most parses.
"""

from __future__ import annotations

import multiprocessing
import queue
from collections.abc import Callable

from parse_service.refusal import PARSE_ERROR, refusal

#: spawn, not fork: the server is multi-threaded, and a forked child inherits
#: locks held by threads that do not exist in it.
_CONTEXT = multiprocessing.get_context("spawn")

#: How long a new worker may take to import the parser and report ready.
_START_TIMEOUT_SECONDS = 120.0


class Busy(Exception):
    """No worker came free in time. The request was not started; retry later."""


def _serve(connection, convert: Callable[[str], dict]) -> None:
    """The worker process: answer one path at a time until the pipe closes."""
    try:
        # Imported for its side effect: pay the import now, not on the first file.
        import pymupdf4llm  # noqa: F401
    except ImportError:
        # Tests run converters that never touch the parser.
        pass
    connection.send("ready")
    while True:
        try:
            path = connection.recv()
        except EOFError:
            return
        connection.send(convert(path))


class _Worker:
    def __init__(self, convert: Callable[[str], dict]) -> None:
        self._convert = convert
        self._start()

    def _start(self) -> None:
        self._connection, child = _CONTEXT.Pipe()
        self._process = _CONTEXT.Process(
            target=_serve, args=(child, self._convert), daemon=True
        )
        self._process.start()
        # The parent's copy must close, or a dead child never reads as EOF.
        child.close()
        if not self._connection.poll(_START_TIMEOUT_SECONDS):
            self.stop()
            raise RuntimeError("a parser worker did not start")
        self._connection.recv()

    def stop(self) -> None:
        self._process.kill()
        self._process.join()
        self._connection.close()

    def alive(self) -> bool:
        return self._process.is_alive()

    def run(self, path: str, timeout_seconds: float) -> dict:
        try:
            self._connection.send(path)
            if not self._connection.poll(timeout_seconds):
                self._replace()
                return refusal(
                    PARSE_ERROR,
                    f"The parser did not finish within {timeout_seconds:g} seconds.",
                )
            return self._connection.recv()
        except (EOFError, BrokenPipeError, ConnectionResetError):
            exit_code = self._process.exitcode
            self._replace()
            return refusal(
                PARSE_ERROR, f"The parser process died on this file (exit {exit_code})."
            )

    def _replace(self) -> None:
        self.stop()
        self._start()


class WorkerPool:
    """``size`` warm workers; ``parse`` borrows one for the length of a file."""

    def __init__(
        self,
        *,
        size: int,
        convert: Callable[[str], dict],
        parse_timeout_seconds: float,
        queue_wait_seconds: float,
    ) -> None:
        self.size = size
        self._parse_timeout = parse_timeout_seconds
        self._queue_wait = queue_wait_seconds
        self._idle: queue.Queue[_Worker] = queue.Queue()
        self._workers = [_Worker(convert) for _ in range(size)]
        for worker in self._workers:
            self._idle.put(worker)

    def parse(self, path: str) -> dict:
        try:
            worker = self._idle.get(timeout=self._queue_wait)
        except queue.Empty:
            raise Busy from None
        try:
            return worker.run(path, self._parse_timeout)
        finally:
            self._idle.put(worker)

    def idle(self) -> int:
        return self._idle.qsize()

    def alive(self) -> int:
        return sum(1 for worker in self._workers if worker.alive())

    def close(self) -> None:
        for worker in self._workers:
            worker.stop()
