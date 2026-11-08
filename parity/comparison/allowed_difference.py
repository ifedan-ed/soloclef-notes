"""The only differences allowed between the two tiers' answers.

Three, and no more: the request id, the error envelope's timestamp, and a value
the answer itself minted while it was being written — a created_at a row did
not have before the request. Everything else is a divergence, including
whitespace, key order and the spelling of a float.

A case opts into the minted-value allowance (`"mint": true`) rather than
getting it by default, because for most routes a created_at is read from a row
and a difference in it would be a real one.
"""

from __future__ import annotations

import re

_REQUEST_ID = re.compile(rb'"request_id":"[^"]*"')
_TIMESTAMP = re.compile(rb'"timestamp":"[^"]*"')
_MINTED_BODY = re.compile(rb'"(created_at|updated_at|joined_at|last_used_at)":"[^"]*"')

# A timestamp as psql prints one, for the rows of a table a write touched.
_MINTED_ROW = re.compile(r"\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(\.\d+)?\+00")


def body(raw: bytes, minted: bool = False) -> bytes:
    masked = _REQUEST_ID.sub(b'"request_id":"<id>"', raw)
    masked = _TIMESTAMP.sub(b'"timestamp":"<ts>"', masked)
    if minted:
        masked = _MINTED_BODY.sub(rb'"\1":"<minted>"', masked)
    return masked


def rows(dump: str, minted: bool = False) -> str:
    return _MINTED_ROW.sub("<minted>", dump) if minted else dump


def headers(read: list[tuple[str, str]]) -> list[tuple[str, str]]:
    """The compared headers, in a fixed order rather than the wire's.

    The order two headers of different names are written in is not something a
    client can read a meaning out of, and the two tiers do differ: uvicorn
    writes the content headers before the ones its middleware added, and Go's
    net/http writes them sorted. Sorting here is therefore not a masked
    difference — but it is a sort and not a mapping, so a header written twice
    is still there twice, which is the one thing about header order that is a
    divergence.
    """
    return sorted(read)
