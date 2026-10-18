import argparse
import asyncio
import logging
import sys

import uvicorn
from dotenv import load_dotenv

_old_log_record_factory = logging.getLogRecordFactory()


def _otel_safe_log_record_factory(*args, **kwargs):
    # Records always carry the OTel ids; the formatter prints trace=<id>
    # once OTel binds a real one.
    record = _old_log_record_factory(*args, **kwargs)
    if not hasattr(record, "otelTraceID"):
        record.otelTraceID = "0"
    if not hasattr(record, "otelSpanID"):
        record.otelSpanID = "0"
    return record


logging.setLogRecordFactory(_otel_safe_log_record_factory)

# Logging next: anything imported below may log (or install a handler) at
# import time, and the first basicConfig-style call wins.
from app.observability.logs import configure_logging  # noqa: E402

configure_logging("api")

from app.config.uvicorn import load_uvicorn_config  # noqa: E402

load_dotenv()

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Run the SurfSense application")
    parser.add_argument("--reload", action="store_true", help="Enable hot reloading")
    args = parser.parse_args()

    config_kwargs = load_uvicorn_config(args)
    config = uvicorn.Config(**config_kwargs)
    server = uvicorn.Server(config)

    if sys.platform == "win32":
        # Windows needs a split-loop setup: psycopg's async driver requires a
        # SelectorEventLoop (no add_reader on Proactor), so the SERVER loop is
        # forced to Selector via loop_factory. The global policy is left at
        # its default (Proactor) so worker threads that call
        # asyncio.new_event_loop() — playwright/patchright sync API used by
        # the scrapers, unstructured, etc. — get a loop that CAN spawn
        # subprocesses. Do not set WindowsSelectorEventLoopPolicy globally:
        # that breaks every browser-based scraper with NotImplementedError.
        asyncio.run(server.serve(), loop_factory=asyncio.SelectorEventLoop)
    else:
        server.run()
