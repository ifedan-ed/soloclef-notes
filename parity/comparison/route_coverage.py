"""Which route a request is, and whether every ported route has been asked.

The suite's claim is "all of them, identically", so it has to be able to fail
for a route nobody wrote a case for. Every concrete request is matched back to
a route template, and a run that leaves a template unasked is a failure with
the template named — not a silently smaller run.
"""

from __future__ import annotations

import json


def load(path: str) -> list[str]:
    with open(path, encoding="utf-8") as listing:
        return list(json.load(listing)["routes"])


def _segments(path: str) -> list[str]:
    return [part for part in path.split("?", 1)[0].split("/") if part != ""]


def match(routes: list[str], method: str, path: str) -> str | None:
    """The template this request is, or None when no ported route answers it.

    A template with more literal segments wins, so `…/memory/reset` is itself
    and not `…/roles/{role_id}` — the same precedence Go's ServeMux and
    Starlette's router each apply.
    """
    asked = _segments(path)
    best, literals_matched = None, -1
    for route in routes:
        wanted_method, template = route.split(" ", 1)
        if wanted_method != method:
            continue
        wanted = _segments(template)
        if len(wanted) != len(asked):
            continue
        literals = 0
        for want, got in zip(wanted, asked):
            if want.startswith("{"):
                continue
            if want != got:
                break
            literals += 1
        else:
            if literals > literals_matched:
                best, literals_matched = route, literals
    return best


def unasked(routes: list[str], asked: set[str]) -> list[str]:
    return [route for route in routes if route not in asked]
