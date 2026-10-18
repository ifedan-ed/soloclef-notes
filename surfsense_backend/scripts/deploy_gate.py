"""Deploy gate: exits non-zero while any SurfSense generation is in flight.

A backend or worker restart kills whatever Studio or chat generation is
running, so a deploy waits until nothing is: no deliverable job queued,
running or cancelling; no chat turn writing a reply; no sandbox container
running; no Celery worker busy with a deliverable, artifact, podcast or video
task; and no podcast or video presentation run drafting or rendering. A probe
that cannot answer counts as busy, so a broken check holds the deploy instead
of waving it through.

Runs on the docker host with the system Python (stdlib only):

    python3 surfsense_backend/scripts/deploy_gate.py          # check once
    python3 surfsense_backend/scripts/deploy_gate.py --wait   # poll 30 s, up to 60 min

Exit 0: clear to deploy. Exit 1: something is in flight (or a probe failed),
or ``--wait`` ran out of time.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import time
from collections.abc import Callable, Sequence
from dataclasses import dataclass

DB_CONTAINER = os.environ.get("SURFSENSE_DB_CONTAINER", "soloclef-db-1")
WORKER_CONTAINER = os.environ.get(
    "SURFSENSE_WORKER_CONTAINER", "soloclef-celery_worker-1"
)
SANDBOX_IMAGE = os.environ.get("SURFSENSE_SANDBOX_IMAGE", "soloclef-sandbox-local")

# Celery task names that are generations; "podcast.render_audio",
# "deliverables.execute_queued", "generate_video_presentation", ...
GENERATION_TASK_MARKERS = ("deliverable", "artifact", "podcast", "video")

# A podcast or video run older than this that still says "in progress" was
# orphaned by an earlier crash; it is reported but does not hold the deploy.
STALE_RUN_HOURS = 6

# A chat turn sets ``chat_session_state.ai_responding_to_user_id`` when it
# starts and clears it when it ends, even when the page that started it is
# gone (turns run in the background). A flag older than this was left by a
# crash and does not hold the deploy.
STALE_CHAT_TURN_MINUTES = 60

IN_FLIGHT_SQL = {
    "deliverable_jobs": (
        "select count(*) from deliverable_jobs "
        "where status in ('queued','running','cancelling')"
    ),
    "chat_session_state": (
        "select count(*) from chat_session_state "
        "where ai_responding_to_user_id is not null "
        f"and updated_at > now() - interval '{STALE_CHAT_TURN_MINUTES} minutes'"
    ),
    "podcast_runs": (
        "select count(*) from podcast_runs where status in ('drafting','rendering') "
        f"and created_at > now() - interval '{STALE_RUN_HOURS} hours'"
    ),
    "video_presentation_runs": (
        "select count(*) from video_presentation_runs "
        "where status in ('pending','generating') "
        f"and created_at > now() - interval '{STALE_RUN_HOURS} hours'"
    ),
}

Runner = Callable[[Sequence[str]], str]


@dataclass(frozen=True)
class Probe:
    name: str
    busy: int | None  # None: the probe could not answer
    detail: str = ""

    @property
    def clear(self) -> bool:
        return self.busy == 0


def run_command(argv: Sequence[str]) -> str:
    result = subprocess.run(
        list(argv), capture_output=True, text=True, timeout=90, check=False
    )
    if result.returncode != 0:
        raise RuntimeError(
            (result.stderr or result.stdout).strip()[-300:] or "command failed"
        )
    return result.stdout


def _psql(sql: str) -> list[str]:
    return [
        "docker",
        "exec",
        DB_CONTAINER,
        "sh",
        "-c",
        'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "$1"',
        "_",
        sql,
    ]


def database_probes(run: Runner) -> list[Probe]:
    probes = []
    for name, sql in IN_FLIGHT_SQL.items():
        try:
            probes.append(Probe(name, int(run(_psql(sql)).strip())))
        except (RuntimeError, ValueError, OSError, subprocess.TimeoutExpired) as exc:
            probes.append(Probe(name, None, str(exc)))
    return probes


def sandbox_probe(run: Runner) -> Probe:
    try:
        out = run(
            [
                "docker",
                "ps",
                "--filter",
                "status=running",
                "--format",
                "{{.Image}}\t{{.Names}}",
            ]
        )
    except (RuntimeError, OSError, subprocess.TimeoutExpired) as exc:
        return Probe("sandboxes", None, str(exc))
    names = [
        line.split("\t", 1)[-1]
        for line in out.splitlines()
        if line.split("\t", 1)[0].split(":", 1)[0] == SANDBOX_IMAGE
    ]
    return Probe("sandboxes", len(names), ", ".join(names))


def active_generation_tasks(inspect_output: str) -> list[str]:
    """Generation task names in ``celery inspect active --json`` output.

    Celery prints worker banners before the JSON, so the last line that
    parses as a JSON object is the reply.
    """
    for line in reversed(inspect_output.strip().splitlines()):
        try:
            reply = json.loads(line)
        except ValueError:
            continue
        if isinstance(reply, dict):
            return [
                task.get("name") or "?"
                for tasks in reply.values()
                for task in (tasks or [])
                if any(
                    m in (task.get("name") or "").lower()
                    for m in GENERATION_TASK_MARKERS
                )
            ]
    raise ValueError("no JSON reply from celery inspect")


def celery_probe(run: Runner) -> Probe:
    try:
        out = run(
            [
                "docker",
                "exec",
                WORKER_CONTAINER,
                "celery",
                "-A",
                "app.celery_app",
                "inspect",
                "active",
                "--json",
                "--timeout",
                "10",
            ]
        )
        tasks = active_generation_tasks(out)
    except (RuntimeError, ValueError, OSError, subprocess.TimeoutExpired) as exc:
        return Probe("celery_tasks", None, str(exc))
    return Probe("celery_tasks", len(tasks), ", ".join(tasks))


def check(run: Runner = run_command) -> list[Probe]:
    return [*database_probes(run), sandbox_probe(run), celery_probe(run)]


def summary(probes: Sequence[Probe]) -> str:
    parts = []
    for p in probes:
        value = "?" if p.busy is None else str(p.busy)
        parts.append(
            f"{p.name}={value}" + (f" ({p.detail})" if p.detail and not p.clear else "")
        )
    return " ".join(parts)


def main(
    argv: Sequence[str] | None = None,
    run: Runner = run_command,
    sleep: Callable[[float], None] = time.sleep,
    clock: Callable[[], float] = time.monotonic,
) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--wait", action="store_true", help="poll until clear")
    parser.add_argument("--interval", type=float, default=30.0)
    parser.add_argument("--timeout", type=float, default=3600.0)
    args = parser.parse_args(argv)

    deadline = clock() + args.timeout
    while True:
        probes = check(run)
        clear = all(p.clear for p in probes)
        print(
            time.strftime("%H:%M:%S"),
            "CLEAR" if clear else "BUSY",
            summary(probes),
            flush=True,
        )
        if clear:
            return 0
        if not args.wait or clock() + args.interval > deadline:
            if args.wait:
                print("deploy gate: timed out waiting", file=sys.stderr)
            return 1
        sleep(args.interval)


if __name__ == "__main__":
    sys.exit(main())
