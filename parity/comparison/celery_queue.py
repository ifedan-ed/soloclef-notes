"""The Celery messages a write enqueued, compared envelope by envelope.

Two routes owe a background task, and the task's envelope is as much part of
the answer as the body is: a worker reads the task name, the positional
arguments and the time limits out of it. A field in the wrong place there is
invisible to every HTTP comparison — which is how the reversed `timelimit`
got as far as it did (parity/cases/regressions.json, "timelimit_is_hard_then_soft").

Masked here is everything Celery itself makes up per message: the task id and
the ids derived from it, the publishing process's name, the enqueue stamp, and
the request id carried for the logs. What is left is what a worker acts on.
"""

from __future__ import annotations

import json
import subprocess

_MASKED_HEADERS = ("id", "root_id", "parent_id", "origin", "surfsense.enqueued_at_ns")
_MASKED_PROPERTIES = ("correlation_id", "reply_to", "delivery_tag")


class Broker:
    def __init__(self, container: str):
        self._container = container

    def drain(self, queues: list[str]) -> list[str]:
        """Read and remove every message on each queue, oldest first.

        Reading and removing are one call into the container, because a case
        drains twice and there are several thousand cases.
        """
        if not queues:
            return []
        script = "; ".join(
            f"redis-cli LRANGE {queue} 0 -1; redis-cli DEL {queue} >/dev/null"
            for queue in queues)
        read = subprocess.run(
            ["docker", "exec", self._container, "sh", "-c", script],
            capture_output=True)
        return [line for line in read.stdout.decode("utf-8", "replace").splitlines()
                if line.strip()]


def masked(envelopes: list[str]) -> str:
    """One comparable text for a request's whole queue traffic."""
    rendered = []
    for raw in envelopes:
        try:
            envelope = json.loads(raw)
        except ValueError:
            rendered.append("undecodable: " + raw)
            continue
        headers = envelope.get("headers")
        if isinstance(headers, dict):
            for field in _MASKED_HEADERS:
                if field in headers:
                    headers[field] = "<masked>"
            context = headers.get("soloclef_log_context")
            if isinstance(context, dict) and "request_id" in context:
                context["request_id"] = "<masked>"
        properties = envelope.get("properties")
        if isinstance(properties, dict):
            for field in _MASKED_PROPERTIES:
                if field in properties:
                    properties[field] = "<masked>"
        rendered.append(json.dumps(envelope, sort_keys=True, ensure_ascii=False))
    return "\n".join(rendered)
