"""Ask both tiers every case and report every difference.

The order is the one a write forces: reseed, ask Python, read its rows and its
queue; reseed, ask Go, read the same. A read is not reseeded, because both
tiers are then asked the same rows whatever an earlier write left behind.

Run it through parity/run.sh, which stands the world up and takes it down
again. Its arguments are case file names to run instead of all of them, which
is what makes it usable while a route is being written.
"""

from __future__ import annotations

import json
import os
import sys

import allowed_difference
import case_files
import celery_queue
import database_rows
import report as reporting
import route_coverage
import tier_request

HERE = os.path.dirname(os.path.abspath(__file__))
PARITY = os.path.dirname(HERE)


def differences(case: dict, python: tier_request.Answer, go: tier_request.Answer,
                python_rows: str, go_rows: str,
                python_queue: str, go_queue: str) -> list[tuple[str, str, str]]:
    minted = bool(case.get("mint"))
    found = []
    if python.status != go.status:
        found.append(("status", str(python.status), str(go.status)))
    python_body = allowed_difference.body(python.body, minted)
    go_body = allowed_difference.body(go.body, minted)
    if python_body != go_body:
        found.append(("body", repr(python_body[:600]), repr(go_body[:600])))
    python_headers = allowed_difference.headers(python.headers)
    go_headers = allowed_difference.headers(go.headers)
    if python_headers != go_headers:
        found.append(("headers", repr(python_headers), repr(go_headers)))
    if allowed_difference.rows(python_rows, minted) != allowed_difference.rows(go_rows, minted):
        found.append(("rows", repr(python_rows[:800]), repr(go_rows[:800])))
    if python_queue != go_queue:
        found.append(("queued", repr(python_queue[:800]), repr(go_queue[:800])))
    return found


def main(argv: list[str]) -> int:
    with open(os.path.join(os.environ["PARITY_STATE_DIR"], "credentials.json"),
              encoding="utf-8") as minted:
        credentials = json.load(minted)

    routes = route_coverage.load(os.path.join(PARITY, "routes.json"))
    suite = case_files.Suite(os.path.join(PARITY, "cases"),
                             os.path.join(PARITY, "matrices"))
    database = database_rows.Database(
        os.environ["PARITY_DB"], os.environ["PARITY_DB_USER"],
        os.environ["PARITY_DB_NAME"],
        os.path.join(PARITY, "stack", "seed.sql"))
    broker = celery_queue.Broker(os.environ["PARITY_REDIS"])
    python_base = os.environ["PARITY_PYTHON_BASE"]
    go_base = os.environ["PARITY_GO_BASE"]

    wanted = argv or None
    files = [name for name in suite.files()
             if wanted is None or os.path.basename(name) in wanted
             or name in wanted]
    if not files:
        print(f"no case file matches {wanted}", file=sys.stderr)
        return 2

    report = reporting.Report()
    asked_routes: set[str] = set()

    for path in files:
        declared, cases = suite.expand(path)
        report.heading(declared or "(no route)", os.path.basename(path), len(cases))
        for case in cases:
            sent = case_files.with_credential(case, credentials)
            matched = route_coverage.match(routes, sent["method"], sent["path"])
            if matched:
                asked_routes.add(matched)
            tables = case.get("tables", [])
            queues = case.get("queues", [])
            write = sent["method"] != "GET" or tables or queues

            if write:
                database.ensure_seeded()
                broker.drain(queues)
            else:
                # A read can still touch a column — a key's last_used_at — so
                # the next write reseeds rather than trusting what is there.
                database.seeded = False
            python = tier_request.ask(python_base, sent)
            python_rows = database.dump_then_reseed(tables) if write else ""
            python_queue = celery_queue.masked(broker.drain(queues))

            go = tier_request.ask(go_base, sent)
            go_rows = database.dump_then_reseed(tables) if write else ""
            go_queue = celery_queue.masked(broker.drain(queues))

            found = differences(case, python, go, python_rows, go_rows,
                                python_queue, go_queue)
            if found:
                report.diverged(declared, case["label"], found)
            else:
                report.identical(case["label"], python.status, len(python.body))

    # Only a full run can claim coverage; a named-file run is a loop, not a
    # proof.
    if wanted is None:
        report.missing_routes(route_coverage.unasked(routes, asked_routes))
    return report.summary()


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
