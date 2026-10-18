"""Application logging: format, context, redaction, filters, request middleware."""

from __future__ import annotations

import asyncio
import json
import logging
import sys
from types import SimpleNamespace

import pytest
from fastapi import FastAPI
from starlette.testclient import TestClient

from app.observability.logs import (
    bind_log_context,
    get_log_context,
    log_context,
    redact,
)
from app.observability.logs.access import RequestContextMiddleware, accept_request_id
from app.observability.logs.filters import (
    KnownNoiseFilter,
    PerfSummaryFilter,
    RepeatFilter,
)
from app.observability.logs.formatters import JsonFormatter, TextFormatter

pytestmark = pytest.mark.unit


def _record(
    msg: str,
    *args,
    name: str = "app.test",
    level: int = logging.INFO,
    exc_info=None,
) -> logging.LogRecord:
    return logging.LogRecord(name, level, __file__, 1, msg, args or None, exc_info)


# ---------------------------------------------------------------- redaction


@pytest.mark.parametrize(
    ("raw", "secret"),
    [
        (
            "calling provider api_key=sk-live-abcdef1234567890",
            "sk-live-abcdef1234567890",
        ),
        ('{"password": "hunter2hunter2", "user": "a"}', "hunter2hunter2"),
        ("{'api_key': 'abc123secretvalue'}", "abc123secretvalue"),
        ("Authorization: Bearer abcdefghijklmnop.qrstu", "abcdefghijklmnop"),
        ("headers={'Authorization': 'Bearer zzzzzzzzzzzzzzzz'}", "zzzzzzzzzzzzzzzz"),
        ("Cookie: session=abc; csrftoken=def", "session=abc"),
        ("connect postgresql://surf:s3cretpw@db:5432/surfsense", "s3cretpw"),
        ("GET /verify?token=abcdef123456&x=1", "abcdef123456"),
        ("key is sk-proj-ABCDEFGHIJKLMNOPQRSTUV", "sk-proj-ABCDEFGHIJKLMNOPQRSTUV"),
        ("pat ss_pat_0123456789abcdef used", "ss_pat_0123456789abcdef"),
        (
            "jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N",
            "eyJzdWIiOiIxMjM0NTY3ODkwIn0",
        ),
        ("client_secret=shh-very-secret&grant_type=x", "shh-very-secret"),
        ("x-api-key: abcdefgh12345678", "abcdefgh12345678"),
        (
            "DELETE /api/v1/push/devices?token=ExponentPushToken%5Bphone-1%5D 204",
            "phone-1",
        ),
        ("push to ExponentPushToken[abcDEF123] failed", "abcDEF123"),
        ("dead token ExpoPushToken[zzz-999]", "zzz-999"),
    ],
)
def test_redact_masks_secrets(raw: str, secret: str) -> None:
    out = redact(raw)
    assert secret not in out
    assert "***" in out


@pytest.mark.parametrize(
    "text",
    [
        "max_tokens=4096 prompt_tokens=12 total_tokens=40",
        "Captured: model=mistral-medium kind=chat prompt=15642 completion=55",
        "GET /api/v1/workspaces/3/notebooks/7 200 12ms",
        "tool failed: tool=web_search type=timeout error=upstream timed out",
    ],
)
def test_redact_leaves_ordinary_lines_alone(text: str) -> None:
    assert redact(text) == text


# ---------------------------------------------------------------- formatters


def test_text_format_has_utc_level_logger_context_and_message() -> None:
    with log_context(request_id="req_abc123def456", workspace_id=3):
        bind_log_context(user_id="1a2b3c4d-0000-0000-0000-000000000000")
        line = TextFormatter().format(_record("hello %s", "world", name="app.foo"))
    assert line.endswith("hello world")
    stamp, level, name = line.split()[:3]
    assert stamp.endswith("Z") and "T" in stamp
    assert level == "INFO"
    assert name == "app.foo"
    assert "[req=req_abc123def456 user=1a2b3c4d ws=3]" in line


def test_text_format_includes_redacted_traceback() -> None:
    try:
        raise RuntimeError("db down: postgresql://u:topsecret@db/x")
    except RuntimeError:
        rec = _record("boom", level=logging.ERROR, exc_info=sys.exc_info())
    line = TextFormatter().format(rec)
    assert "Traceback" in line and "RuntimeError" in line
    assert "topsecret" not in line


def test_json_format_is_one_valid_object_with_context() -> None:
    with log_context(request_id="req_000000000001", thread_id=86):
        out = JsonFormatter().format(_record('said "hi" password=abc123xyz'))
    payload = json.loads(out)
    assert payload["level"] == "INFO"
    assert payload["request_id"] == "req_000000000001"
    assert payload["thread_id"] == "86"
    assert payload["ts"].endswith("Z")
    assert "abc123xyz" not in payload["msg"]
    assert "\n" not in out


def test_context_values_cannot_inject_lines() -> None:
    with log_context(request_id="req_x\nFAKE ERROR line"):
        line = TextFormatter().format(_record("m"))
    assert "\n" not in line


# ---------------------------------------------------------------- context


def test_bound_fields_are_visible_across_task_copies() -> None:
    async def child() -> None:
        # Runs in a *copy* of the context, like a dependency or background task.
        bind_log_context(user_id="u-1")

    async def main() -> dict[str, str]:
        with log_context(request_id="req_parent00001"):
            await asyncio.create_task(child())
            return get_log_context()

    ctx = asyncio.run(main())
    assert ctx == {"request_id": "req_parent00001", "user_id": "u-1"}
    assert get_log_context() == {}


def test_accept_request_id_rejects_malformed_values() -> None:
    assert accept_request_id("req_abcdef123456") == "req_abcdef123456"
    for bad in (None, "", "x", "a b", "a\nb", "x" * 65, "<script>"):
        minted = accept_request_id(bad)
        assert minted.startswith("req_") and len(minted) == 16


# ---------------------------------------------------------------- filters


@pytest.mark.parametrize(
    ("mode", "message", "shown"),
    [
        ("summary", "[pack_subagent] name=amazon tools=4", False),
        ("summary", "[mcp_discover] connector=4 tools=6", False),
        ("summary", "[stream_new_chat] LLM config loaded in 0.030s", False),
        ("summary", "[stream_new_chat] Total pre-stream setup in 0.5s", True),
        ("summary", "[finalize_assistant_turn] outcome=ok chat_id=86", True),
        ("summary", "[slow_request] rss=1002MB", True),
        ("summary", "[hitl_route] atask EXIT subagent_type='kb'", True),
        ("all", "[pack_subagent] name=amazon tools=4", True),
        ("off", "[finalize_assistant_turn] outcome=ok", False),
    ],
)
def test_perf_summary_filter(mode: str, message: str, shown: bool) -> None:
    flt = PerfSummaryFilter(mode)
    assert flt.filter(_record(message, name="surfsense.perf")) is shown


def test_perf_filter_never_hides_warnings() -> None:
    rec = _record("[pack_subagent] broke", name="surfsense.perf", level=logging.WARNING)
    assert PerfSummaryFilter("off").filter(rec)


def test_repeat_filter_logs_first_occurrence_per_key() -> None:
    flt = RepeatFilter()
    name = "langgraph.checkpoint.serde.jsonplus"
    first = _record(
        "Deserializing unregistered type a.B from checkpoint.",
        name=name,
        level=logging.WARNING,
    )
    assert flt.filter(first)
    assert "further repeats suppressed" in first.getMessage()
    again = _record(
        "Deserializing unregistered type a.B from checkpoint.",
        name=name,
        level=logging.WARNING,
    )
    assert not flt.filter(again)
    other = _record(
        "Deserializing unregistered type a.C from checkpoint.",
        name=name,
        level=logging.WARNING,
    )
    assert flt.filter(other)
    unrelated = _record("something else", name=name, level=logging.WARNING)
    assert flt.filter(unrelated) and flt.filter(unrelated)


def test_repeat_filter_strips_tokenizer_traceback() -> None:
    try:
        raise KeyError("Could not automatically map local-bge-m3")
    except KeyError:
        rec = _record(
            "Failed to initialize tokenizer: x. Using cl100k_base fallback.",
            name="chonkie.embeddings.litellm",
            level=logging.WARNING,
            exc_info=sys.exc_info(),
        )
    assert RepeatFilter().filter(rec)
    assert rec.exc_info is None


@pytest.mark.parametrize(
    ("name", "message", "shown"),
    [
        (
            "celery.worker.strategy",
            "Task automation_schedule_select[ab-12] received",
            False,
        ),
        (
            "celery.app.trace",
            "Task deliverables.reconcile_queued[ab-12] succeeded in 0.1s: 0",
            False,
        ),
        (
            "celery.beat",
            "Scheduler: Sending due task automation-schedule-select (automation_schedule_select)",
            False,
        ),
        (
            "celery.app.trace",
            "Task deliverables.execute[ab-12] succeeded in 9s: {}",
            True,
        ),
        ("celery.worker.strategy", "Task deliverables.execute[ab-12] received", True),
        ("celery.redirected", "Info: No global LLM configs found; pool off", False),
        (
            "app.services.pricing_registration",
            "[PricingRegistration] no global configs to register",
            False,
        ),
    ],
)
def test_known_noise_filter_in_worker(name: str, message: str, shown: bool) -> None:
    level = logging.WARNING if name == "celery.redirected" else logging.INFO
    assert (
        KnownNoiseFilter("worker").filter(_record(message, name=name, level=level))
        is shown
    )


def test_quiet_task_failures_still_show() -> None:
    rec = _record(
        "Task automation_schedule_select[ab] raised unexpected: X",
        name="celery.app.trace",
        level=logging.ERROR,
    )
    assert KnownNoiseFilter("worker").filter(rec)


# ---------------------------------------------------------------- middleware


def _app() -> FastAPI:
    app = FastAPI()
    seen: dict[str, dict[str, str]] = {}

    @app.get("/api/v1/workspaces/{ws}/thing")
    async def thing(ws: int) -> dict:
        seen["ctx"] = get_log_context()
        logging.getLogger("app.route").info("inside")
        return {"ok": True}

    @app.get("/ready")
    async def ready() -> dict:
        return {"ok": True}

    @app.get("/boom")
    async def boom() -> dict:
        raise RuntimeError("nope")

    app.add_middleware(RequestContextMiddleware)
    app.state.seen = seen
    return app


def test_middleware_sets_id_context_and_access_line(caplog) -> None:
    app = _app()
    client = TestClient(app)
    with caplog.at_level(logging.INFO):
        res = client.get("/api/v1/workspaces/3/thing?token=abc123456789")
    rid = res.headers["x-request-id"]
    assert rid.startswith("req_")
    assert app.state.seen["ctx"] == {"request_id": rid, "workspace_id": "3"}
    access = [r for r in caplog.records if r.name == "surfsense.access"]
    assert len(access) == 1
    assert "GET /api/v1/workspaces/3/thing" in access[0].getMessage()
    assert " 200 " in access[0].getMessage()
    assert "abc123456789" not in TextFormatter().format(access[0])
    assert get_log_context() == {}


def test_middleware_keeps_valid_caller_id_and_replaces_bad_one() -> None:
    client = TestClient(_app())
    good = client.get("/ready", headers={"X-Request-ID": "req_fromcaller01"})
    assert good.headers["x-request-id"] == "req_fromcaller01"
    bad = client.get("/ready", headers={"X-Request-ID": "evil value\twith tab"})
    assert bad.headers["x-request-id"] != "evil value\twith tab"


def test_middleware_skips_healthy_probes_but_logs_5xx(caplog) -> None:
    client = TestClient(_app(), raise_server_exceptions=False)
    with caplog.at_level(logging.INFO):
        client.get("/ready")
        client.get("/boom")
    access = [r.getMessage() for r in caplog.records if r.name == "surfsense.access"]
    assert not any("/ready" in m for m in access)
    boom = [
        r
        for r in caplog.records
        if r.name == "surfsense.access" and "/boom" in r.getMessage()
    ]
    assert (
        boom and boom[0].levelno == logging.WARNING and " 500 " in boom[0].getMessage()
    )


# ---------------------------------------------------------------- celery


def test_celery_publish_carries_request_context_into_task() -> None:
    from app.observability.logs import celery_signals as sig

    headers: dict = {}
    with log_context(request_id="req_publisher001", user_id="u-9", workspace_id=4):
        sig._propagate_context(headers=headers)
    assert headers[sig._HEADER] == {
        "request_id": "req_publisher001",
        "user_id": "u-9",
        "workspace_id": "4",
    }

    task = SimpleNamespace(
        name="deliverables.execute",
        request=SimpleNamespace(headers=headers),
    )
    sig._enter_task_context(task_id="0123456789abcdef", task=task)
    try:
        ctx = get_log_context()
        assert ctx["task"] == "deliverables.execute"
        assert ctx["task_id"] == "01234567"
        assert ctx["request_id"] == "req_publisher001"
    finally:
        sig._exit_task_context(task_id="0123456789abcdef")
    assert get_log_context() == {}
