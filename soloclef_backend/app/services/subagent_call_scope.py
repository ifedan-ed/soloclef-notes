"""Marks model calls made on a subagent's behalf.

A subagent's reply goes to the main agent, never straight to the user, so
per-call signals about user-visible output (the cut-off marker) must skip
these calls. Context-local, so it follows the subagent's tasks and callbacks.
"""

from __future__ import annotations

from collections.abc import Iterator
from contextlib import contextmanager
from contextvars import ContextVar

_inside_subagent: ContextVar[bool] = ContextVar("inside_subagent", default=False)


def is_inside_subagent() -> bool:
    return _inside_subagent.get()


@contextmanager
def subagent_scope() -> Iterator[None]:
    token = _inside_subagent.set(True)
    try:
        yield
    finally:
        _inside_subagent.reset(token)
