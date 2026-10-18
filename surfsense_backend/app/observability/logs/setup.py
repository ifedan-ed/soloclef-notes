"""Install the SurfSense log pipeline on the root logger.

Called from ``main.py`` (API, before anything else is imported), again from the
FastAPI lifespan (libraries imported after the first call — LiteLLM, chonkie —
attach their own handlers at import time, so the second pass takes them back),
and from Celery's ``setup_logging`` signal (worker and beat), which also stops
Celery from installing its own format.

Environment:
    LOG_LEVEL   root level (default INFO)
    LOG_FORMAT  ``text`` (default) or ``json``
    PERF_LOG    ``summary`` (default), ``all`` or ``off`` — see filters.py
    LOG_LIBRARY_DEBUG  set to 1 to let LiteLLM/httpx/etc. log at INFO again
"""

from __future__ import annotations

import logging
import os
import sys

# chonkie installs its own handler + format at import unless told not to.
os.environ.setdefault("CHONKIE_LOG", "unconfigured")

from .filters import KnownNoiseFilter, PerfSummaryFilter, RepeatFilter
from .formatters import JsonFormatter, TextFormatter

_HANDLER_MARK = "_surfsense_log_handler"

# Third-party loggers that are chatty at INFO. They still report WARNING+.
_LIBRARY_LEVELS: dict[str, int] = {
    "LiteLLM": logging.WARNING,
    "LiteLLM Router": logging.WARNING,
    "LiteLLM Proxy": logging.WARNING,
    "litellm": logging.WARNING,
    "httpx": logging.WARNING,
    "httpcore": logging.WARNING,
    "openai": logging.WARNING,
    "urllib3": logging.WARNING,
    "botocore": logging.WARNING,
    "mcp.client": logging.WARNING,
    "chonkie": logging.WARNING,
    "chonkie.chunker.code": logging.ERROR,
    "celery.worker.autoscale": logging.WARNING,
    "apscheduler": logging.WARNING,
    "uvicorn.access": logging.WARNING,
}
# Loggers that install their own StreamHandler; we strip it so their records
# reach the root handler once, in our format, instead of twice in theirs.
_OWN_HANDLER_LOGGERS = (
    "LiteLLM",
    "LiteLLM Router",
    "LiteLLM Proxy",
    "chonkie",
    "uvicorn",
    "uvicorn.error",
    "uvicorn.access",
    "surfsense.perf",
    "surfsense.perf.slow",
)

_repeat_filter = RepeatFilter()


def _level(name: str, default: str = "INFO") -> int:
    value = logging.getLevelName(os.environ.get(name, default).strip().upper())
    return value if isinstance(value, int) else logging.INFO


def _truthy(name: str) -> bool:
    return os.environ.get(name, "").strip().lower() in {"1", "true", "yes", "on"}


def build_handler(service: str) -> logging.Handler:
    handler = logging.StreamHandler(sys.stdout)
    fmt = os.environ.get("LOG_FORMAT", "text").strip().lower()
    handler.setFormatter(JsonFormatter() if fmt == "json" else TextFormatter())
    handler.addFilter(KnownNoiseFilter(service))
    handler.addFilter(_repeat_filter)
    setattr(handler, _HANDLER_MARK, True)
    return handler


def configure_logging(service: str = "api") -> None:
    """Idempotent: safe to call again after late imports."""
    root = logging.getLogger()
    # Replace plain stream handlers (ours, basicConfig's, Celery's); keep
    # anything else, e.g. the OTel LoggingHandler when OTLP export is on.
    for handler in list(root.handlers):
        if (
            getattr(handler, _HANDLER_MARK, False)
            or type(handler) is logging.StreamHandler
        ):
            root.removeHandler(handler)
    root.addHandler(build_handler(service))
    root.setLevel(_level("LOG_LEVEL"))

    for name in _OWN_HANDLER_LOGGERS:
        lg = logging.getLogger(name)
        for handler in list(lg.handlers):
            lg.removeHandler(handler)
        lg.propagate = True

    if not _truthy("LOG_LIBRARY_DEBUG"):
        for name, level in _LIBRARY_LEVELS.items():
            logging.getLogger(name).setLevel(level)

    perf = logging.getLogger("surfsense.perf")
    perf.setLevel(logging.DEBUG)
    for flt in list(perf.filters):
        if isinstance(flt, PerfSummaryFilter):
            perf.removeFilter(flt)
    perf.addFilter(PerfSummaryFilter())

    # Route warnings.warn(...) through logging so they get the format, the
    # repeat filter and redaction.
    logging.captureWarnings(True)

    _quiet_litellm()


def _quiet_litellm() -> None:
    """Stop LiteLLM printing its 'Provider List' / feedback banners to stdout."""
    if _truthy("LOG_LIBRARY_DEBUG"):
        return
    litellm = sys.modules.get("litellm")
    if litellm is None:
        return
    try:
        litellm.suppress_debug_info = True
        litellm.set_verbose = False
    except Exception:  # pragma: no cover - defensive
        pass
