"""Stand-in converters for the worker tests.

They live in an importable module because a worker is a spawned process: it
finds its converter by importing it, which a function defined inside a test
cannot offer.
"""

from __future__ import annotations

import os
import time


def echo(path: str) -> dict:
    return {"pages": [{"number": 1, "markdown": path}]}


def die(path: str) -> dict:
    # What a MuPDF segfault looks like from outside: the process is gone.
    os._exit(11)


def die_on_bad(path: str) -> dict:
    if path == "bad":
        os._exit(11)
    return echo(path)


def hang(path: str) -> dict:
    time.sleep(60)
    return echo(path)


def slow(path: str) -> dict:
    time.sleep(1.0)
    return echo(path)
