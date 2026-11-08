"""Filters that keep the logs about what happened rather than who is idle.

* :class:`PerfSummaryFilter` — the ``[PERF]`` logger emits ~40 lines per chat
  turn (one per subagent packed, per MCP connector, per setup stage). By
  default only the per-turn / per-job summaries pass; ``PERF_LOG=all`` brings
  the rest back for profiling, ``PERF_LOG=off`` silences it.
* :class:`RepeatFilter` — known-harmless warnings that repeat on every turn are
  logged the first time (per process) and then suppressed.
* :class:`KnownNoiseFilter` — housekeeping Celery tasks that run every minute
  and succeed, plus a few one-off library quirks.
"""

from __future__ import annotations

import logging
import os
import re
import threading

# --------------------------------------------------------------------------
# [PERF]
# --------------------------------------------------------------------------

# (label, required substring or "") pairs that stay visible in summary mode.
_PERF_SUMMARY: tuple[tuple[str, str], ...] = (
    ("startup_complete", ""),
    ("slow_request", ""),
    ("finalize_assistant_turn", ""),
    ("token_usage", ""),
    ("stream_new_chat", "Total pre-stream setup"),
    ("stream_new_chat", "First agent event"),
    ("hitl_route", "EXIT"),
    ("indexing", "index TOTAL"),
)
_LABEL = re.compile(r"^\s*\[([A-Za-z0-9_.:-]+)\]")


def perf_mode() -> str:
    mode = os.environ.get("PERF_LOG", "summary").strip().lower()
    return mode if mode in {"summary", "all", "off"} else "summary"


class PerfSummaryFilter(logging.Filter):
    def __init__(self, mode: str | None = None) -> None:
        super().__init__()
        self.mode = mode or perf_mode()

    def filter(self, record: logging.LogRecord) -> bool:
        if self.mode == "all" or record.levelno >= logging.WARNING:
            return True
        if self.mode == "off":
            return False
        message = record.getMessage()
        match = _LABEL.match(message)
        if match is None:
            return False
        label = match.group(1)
        return any(
            label == want and (not needle or needle in message)
            for want, needle in _PERF_SUMMARY
        )


# --------------------------------------------------------------------------
# Repeats
# --------------------------------------------------------------------------

# (logger prefix, regex). The first capture group, if any, is part of the
# dedupe key so e.g. each unregistered checkpoint type is reported once.
_REPEATS: tuple[tuple[str, re.Pattern[str]], ...] = (
    (
        "langgraph.checkpoint.serde",
        re.compile(r"Deserializing unregistered type (\S+)"),
    ),
    (
        "app.services.token_tracking_service",
        re.compile(r"No cost resolved for model=(\S+)"),
    ),
    ("py.warnings", re.compile(r"(Pydantic serializer warnings)")),
    ("chonkie", re.compile(r"(Failed to initialize tokenizer)")),
    ("chonkie", re.compile(r"(The language is set to `auto`)")),
    ("", re.compile(r"(Stripe reconciliation skipped)")),
)


class RepeatFilter(logging.Filter):
    def __init__(self) -> None:
        super().__init__()
        self._seen: set[tuple[str, str]] = set()
        self._lock = threading.Lock()

    def reset(self) -> None:
        with self._lock:
            self._seen.clear()

    def filter(self, record: logging.LogRecord) -> bool:
        if record.levelno >= logging.ERROR:
            return True
        for prefix, pattern in _REPEATS:
            if prefix and not record.name.startswith(prefix):
                continue
            match = pattern.search(record.getMessage())
            if match is None:
                continue
            key = (pattern.pattern, match.group(1) if match.groups() else "")
            with self._lock:
                if key in self._seen:
                    return False
                self._seen.add(key)
            # Say so once, so nobody wonders why it stopped.
            record.msg = f"{record.getMessage().rstrip()} (further repeats suppressed)"
            record.args = None
            # The tokenizer fallback carries a 10-line KeyError traceback that
            # says nothing the message doesn't.
            if record.name.startswith("chonkie"):
                record.exc_info = None
                record.exc_text = None
            return True
        return True


# --------------------------------------------------------------------------
# Celery housekeeping and library noise
# --------------------------------------------------------------------------

# Beat schedules these every minute or so. Their success is not news; their
# failure still logs (celery.app.trace reports failures at ERROR).
QUIET_TASKS = frozenset(
    {
        "deliverables.reconcile_queued",
        "automation_schedule_select",
        "check_periodic_schedules",
        "cleanup_stale_indexing_notifications",
    }
)
_CELERY_TASK_LINE = re.compile(
    r"^(?:Task (?P<name>[\w.:-]+)\[[^\]]*\] (?:received|succeeded)"
    r"|Scheduler: Sending due task \S+ \((?P<beat>[\w.:-]+)\))"
)
# Printed by every freshly forked worker child (the autoscaler forks often).
_WORKER_BOOT_CHATTER = (
    "Info: No global LLM configs found",
    "Info: No global image generation configs found",
    "[PricingRegistration] no global configs to register",
)


def _quiet_tasks() -> frozenset[str]:
    extra = os.environ.get("LOG_QUIET_TASKS", "")
    return QUIET_TASKS | {t.strip() for t in extra.split(",") if t.strip()}


class KnownNoiseFilter(logging.Filter):
    def __init__(self, service: str = "api") -> None:
        super().__init__()
        self.service = service
        self.quiet_tasks = _quiet_tasks()

    def filter(self, record: logging.LogRecord) -> bool:
        if record.levelno >= logging.WARNING and not record.name.startswith(
            "celery.redirected"
        ):
            return True
        name = record.name
        if name.startswith("celery"):
            message = record.getMessage()
            match = _CELERY_TASK_LINE.match(message)
            if match is not None:
                task = match.group("name") or match.group("beat")
                if task in self.quiet_tasks:
                    return False
            return not (
                self.service != "api" and message.startswith(_WORKER_BOOT_CHATTER)
            )
        if self.service != "api":
            return not record.getMessage().startswith(_WORKER_BOOT_CHATTER)
        return True
