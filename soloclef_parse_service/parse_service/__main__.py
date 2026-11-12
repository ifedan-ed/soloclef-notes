"""``python -m parse_service``: read the settings, warm the workers, serve."""

from __future__ import annotations

import os
import signal
import sys

from parse_service.convert import to_pages
from parse_service.http_api import ParseServer
from parse_service.settings import SettingError, load
from parse_service.workers import WorkerPool


def main() -> int:
    try:
        settings = load(os.environ)
    except SettingError as exc:
        print(f"parse_service: {exc}", file=sys.stderr)
        return 2
    pool = WorkerPool(
        size=settings.workers,
        convert=to_pages,
        parse_timeout_seconds=settings.parse_timeout_seconds,
        queue_wait_seconds=settings.queue_wait_seconds,
    )
    server = ParseServer((settings.host, settings.port), pool, settings.max_bytes)

    def stop(*_: object) -> None:
        # docker stop sends SIGTERM; leave through serve_forever's finally.
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, stop)
    print(
        f"parse_service: listening on {settings.host}:{settings.port} "
        f"with {settings.workers} worker(s)",
        file=sys.stderr,
        flush=True,
    )
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
        pool.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
