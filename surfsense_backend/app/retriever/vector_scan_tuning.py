"""Let one search's HNSW scan run until its LIMIT is satisfied.

A filtered vector search asks the index for the nearest chunks and then keeps
only the ones in the caller's workspace. Before pgvector 0.8 the index handed
back a single fixed-size candidate list; if the filter emptied it the query
simply returned fewer rows than it asked for, with nothing logged and no error
raised. ``hnsw.iterative_scan`` makes the scan resumable so the filter can be
satisfied, and ``hnsw.max_scan_tuples`` bounds what that is allowed to cost.

The GUCs are set with ``SET LOCAL``, so they expire with the transaction and
cannot leak onto the next request sharing the pooled connection. They must be
set before the query they tune, inside the same transaction.
"""

from __future__ import annotations

from sqlalchemy import text

# Only these values are passed through to the GUC. The setting is interpolated
# into SQL (GUCs take no bind parameters), so it is never caller-controlled.
_ITERATIVE_SCAN_MODES = frozenset({"off", "relaxed_order", "strict_order"})


async def tune_vector_scan(db_session) -> None:
    """Apply this deployment's pgvector scan settings to the open transaction."""
    from app.config import config

    mode = str(config.VECTOR_SEARCH_ITERATIVE_SCAN).strip().lower()
    if mode not in _ITERATIVE_SCAN_MODES:
        raise ValueError(
            f"VECTOR_SEARCH_ITERATIVE_SCAN={config.VECTOR_SEARCH_ITERATIVE_SCAN!r} "
            f"is not one of {sorted(_ITERATIVE_SCAN_MODES)}"
        )
    max_scan_tuples = int(config.VECTOR_SEARCH_MAX_SCAN_TUPLES)
    if max_scan_tuples < 1:
        raise ValueError(
            f"VECTOR_SEARCH_MAX_SCAN_TUPLES={max_scan_tuples} must be at least 1"
        )

    await db_session.execute(text(f"SET LOCAL hnsw.iterative_scan = '{mode}'"))
    await db_session.execute(
        text(f"SET LOCAL hnsw.max_scan_tuples = {max_scan_tuples}")
    )


__all__ = ["tune_vector_scan"]
