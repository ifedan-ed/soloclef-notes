"""Read-only check: every stored LangGraph checkpoint loads under the strict serializer.

``app.agents.chat.runtime.checkpoint_serde`` registers the classes SurfSense
keeps in graph state, which puts langgraph's serializer in strict mode (only
built-in safe types plus that allowlist are rebuilt). Run this against a
database before shipping a change to that list, or before a langgraph upgrade:

1. Enumerate: decode every ``checkpoint_blobs`` / ``checkpoint_writes`` row
   without rebuilding anything and collect each ``module.class`` (and
   ``module.class.method``) the msgpack ext records name, plus any LangChain
   JSON constructor in the ``checkpoints`` JSONB.
2. Verify row by row: load each blob with the strict serializer and with the
   permissive one (allow all) and require the same value, no "blocked" serde
   event and no exception.
3. Verify through the saver: list every checkpoint (with its pending writes)
   through ``AsyncPostgresSaver`` built with the strict serializer.

``DATABASE_URL`` must be set (the app's own modules are imported to rebuild
the registered classes, and the check reads from that database). The
connection is opened with ``default_transaction_read_only=on`` and
``setup()`` is never called, so nothing is written.

    docker run --rm --network surfsense_default \\
      --env-file <(docker exec soloclef-backend-1 printenv | grep ^DATABASE_URL=) \\
      --env-file docker/.env -e PYTHONPATH=/app -w /tmp \\
      -v "$PWD/surfsense_backend:/app:ro" --entrypoint python \\
      surfsense-backend-test:latest /app/scripts/verify_checkpoint_serde.py

Exit 0 when every row and checkpoint loads cleanly, 1 otherwise.
"""

from __future__ import annotations

import asyncio
import collections
import importlib
import importlib.util
import json
import logging
import os
import sys
from pathlib import Path
from typing import Any

import ormsgpack
from langgraph.checkpoint.postgres.aio import AsyncPostgresSaver
from langgraph.checkpoint.serde import _msgpack as lg_msgpack
from langgraph.checkpoint.serde.event_hooks import register_serde_event_listener
from langgraph.checkpoint.serde.jsonplus import (
    EXT_CONSTRUCTOR_KW_ARGS,
    EXT_CONSTRUCTOR_POS_ARGS,
    EXT_CONSTRUCTOR_SINGLE_ARG,
    EXT_METHOD_SINGLE_ARG,
    EXT_PYDANTIC_V1,
    EXT_PYDANTIC_V2,
    JsonPlusSerializer,
)
from psycopg import AsyncConnection
from psycopg.rows import dict_row

_CONSTRUCTOR_CODES = {
    EXT_CONSTRUCTOR_SINGLE_ARG,
    EXT_CONSTRUCTOR_POS_ARGS,
    EXT_CONSTRUCTOR_KW_ARGS,
    EXT_PYDANTIC_V1,
    EXT_PYDANTIC_V2,
}


def _load_serde_module():
    """Import checkpoint_serde by path so the app package (and its config) stays unloaded."""
    path = (
        Path(__file__).resolve().parents[1]
        / "app/agents/chat/runtime/checkpoint_serde.py"
    )
    spec = importlib.util.spec_from_file_location("checkpoint_serde", path)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def _conninfo() -> str:
    url = os.environ["DATABASE_URL"]
    for prefix in ("postgresql+asyncpg://", "postgresql+psycopg://"):
        if url.startswith(prefix):
            url = "postgresql://" + url[len(prefix) :]
    return url


def _record_types(blob: bytes, seen: collections.Counter) -> None:
    """Walk the msgpack ext records, counting the types they name; build nothing."""

    def hook(code: int, data: bytes) -> Any:
        tup = ormsgpack.unpackb(data, ext_hook=hook, option=ormsgpack.OPT_NON_STR_KEYS)
        if code in _CONSTRUCTOR_CODES:
            seen[(tup[0], tup[1])] += 1
        elif code == EXT_METHOD_SINGLE_ARG:
            seen[(tup[0], tup[1], tup[3])] += 1
        else:
            seen[(f"<ext {code}>",)] += 1
        return None

    ormsgpack.unpackb(blob, ext_hook=hook, option=ormsgpack.OPT_NON_STR_KEYS)


def _record_json_constructors(value: Any, seen: collections.Counter) -> None:
    if isinstance(value, dict):
        if value.get("lc") in (1, 2) and value.get("type") == "constructor":
            seen[tuple(value.get("id") or ())] += 1
        for item in value.values():
            _record_json_constructors(item, seen)
    elif isinstance(value, list):
        for item in value:
            _record_json_constructors(item, seen)


def _is_allowed(key: tuple[str, ...], allowlist: frozenset) -> bool:
    if len(key) == 3:
        return key in lg_msgpack.SAFE_MSGPACK_METHODS
    return key in lg_msgpack.SAFE_MSGPACK_TYPES or key in allowlist


async def main() -> int:
    serde_module = _load_serde_module()
    allowlist = serde_module.SURFSENSE_CHECKPOINT_TYPES
    strict = serde_module.checkpoint_serde()
    # Silence langgraph's per-type warnings from the permissive reference load.
    logging.getLogger("langgraph.checkpoint.serde.jsonplus").setLevel(logging.ERROR)
    permissive = JsonPlusSerializer(allowed_msgpack_modules=True)

    # The ext hook imports a class's module on first use and, if that import
    # fails, silently returns the raw fields. Import every registered module
    # up front so a broken import is reported instead of hidden.
    import_failures = []
    for module_name, class_name in sorted(allowlist):
        try:
            getattr(importlib.import_module(module_name), class_name)
        except Exception as exc:
            import_failures.append(f"{module_name}.{class_name}: {exc!r}")
    if import_failures:
        print("Registered types that do not import:", *import_failures, sep="\n  ")
        return 1

    events: collections.Counter = collections.Counter()
    unregister = register_serde_event_listener(
        lambda event: events.update(
            [(event["kind"], event["module"], event["name"], event.get("method", ""))]
        )
    )

    conn = await AsyncConnection.connect(
        _conninfo(),
        autocommit=True,
        prepare_threshold=0,
        row_factory=dict_row,
        options="-c default_transaction_read_only=on",
    )
    failures: list[str] = []
    try:
        # --- 1 + 2: every stored value, row by row -------------------------
        row_counts: dict[str, collections.Counter] = {}
        type_counts: dict[str, collections.Counter] = {}
        for table, key_cols in (
            ("checkpoint_blobs", "thread_id, checkpoint_ns, channel, version"),
            (
                "checkpoint_writes",
                "thread_id, checkpoint_ns, checkpoint_id, task_id, idx",
            ),
        ):
            rows_by_tag: collections.Counter = collections.Counter()
            seen: collections.Counter = collections.Counter()
            cursor = await conn.execute(f"SELECT {key_cols}, type, blob FROM {table}")
            async for row in cursor:
                tag = row["type"]
                blob = bytes(row["blob"]) if row["blob"] is not None else b""
                rows_by_tag[tag] += 1
                where = f"{table} {[row[c.strip()] for c in key_cols.split(',')]}"
                if tag == "msgpack":
                    _record_types(blob, seen)
                elif tag == "json":
                    _record_json_constructors(json.loads(blob), seen)
                before = sum(
                    v
                    for k, v in events.items()
                    if k[0] != "msgpack_unregistered_allowed"
                )
                try:
                    got = strict.loads_typed((tag, blob))
                    want = permissive.loads_typed((tag, blob))
                except Exception as exc:
                    failures.append(f"{where}: {type(exc).__name__}: {exc}")
                    continue
                after = sum(
                    v
                    for k, v in events.items()
                    if k[0] != "msgpack_unregistered_allowed"
                )
                if after != before:
                    failures.append(f"{where}: serde blocked a type")
                elif got != want:
                    failures.append(f"{where}: strict load differs from permissive")
            row_counts[table] = rows_by_tag
            type_counts[table] = seen

        json_seen: collections.Counter = collections.Counter()
        checkpoint_rows = 0
        cursor = await conn.execute("SELECT checkpoint, metadata FROM checkpoints")
        async for row in cursor:
            checkpoint_rows += 1
            _record_json_constructors(row["checkpoint"], json_seen)
            _record_json_constructors(row["metadata"], json_seen)

        # --- 3: every checkpoint through the saver --------------------------
        saver = AsyncPostgresSaver(conn=conn, serde=strict)
        before = sum(
            v for k, v in events.items() if k[0] != "msgpack_unregistered_allowed"
        )
        loaded = pending_writes = 0
        try:
            async for item in saver.alist(None):
                loaded += 1
                pending_writes += len(item.pending_writes or ())
        except Exception as exc:
            failures.append(f"saver.alist: {type(exc).__name__}: {exc}")
        after = sum(
            v for k, v in events.items() if k[0] != "msgpack_unregistered_allowed"
        )
        if after != before:
            failures.append(f"saver.alist: {after - before} blocked serde events")
    finally:
        await conn.close()
        unregister()

    print("Rows by storage type:")
    for table, counts in row_counts.items():
        print(f"  {table}: {dict(counts)} (total {sum(counts.values())})")
    print(f"  checkpoints: {checkpoint_rows}")
    print("\nTypes named in stored msgpack values (occurrences):")
    merged: collections.Counter = collections.Counter()
    for counts in type_counts.values():
        merged.update(counts)
    unlisted = []
    for key, count in sorted(merged.items(), key=lambda kv: (-kv[1], kv[0])):
        source = (
            "surfsense allowlist"
            if key in allowlist
            else "langgraph built-in"
            if _is_allowed(key, frozenset())
            else "NOT ALLOWED"
        )
        if source == "NOT ALLOWED":
            unlisted.append(key)
        per_table = ", ".join(
            f"{table}={counts[key]}"
            for table, counts in type_counts.items()
            if counts[key]
        )
        print(f"  {'.'.join(key)}: {count} ({per_table}) [{source}]")
    if json_seen:
        print("\nLangChain JSON constructors in checkpoints JSONB:")
        for key, count in sorted(json_seen.items()):
            print(f"  {'.'.join(map(str, key))}: {count}")
    print(
        f"\nSaver (strict serde): {loaded} checkpoints loaded, {pending_writes} pending writes"
    )
    blocked = {
        k: v for k, v in events.items() if k[0] != "msgpack_unregistered_allowed"
    }
    print(f"Serde blocked events: {sum(blocked.values())}")
    for key, count in blocked.items():
        print(f"  {key}: {count}")
    for key in unlisted:
        failures.append(f"type not allowed: {'.'.join(key)}")
    print(f"Failures: {len(failures)}")
    for failure in failures[:50]:
        print(f"  {failure}")
    return 0 if not failures else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
