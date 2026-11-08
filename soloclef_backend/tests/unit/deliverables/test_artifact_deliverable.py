"""Queued ``artifact`` jobs run the deliverables subagent headlessly on their own thread."""

from __future__ import annotations

from contextlib import asynccontextmanager
from types import SimpleNamespace

import pytest

from app.deliverables.artifact import executor
from app.deliverables.artifact.progress import JobProgressHeartbeat
from app.deliverables.jobs.policy import ARTIFACT_KIND
from app.tasks.celery_tasks import deliverable_job_tasks as tasks

pytestmark = pytest.mark.unit


def _job(**overrides) -> SimpleNamespace:
    values = {
        "id": 21,
        "kind": ARTIFACT_KIND,
        "title": "Quiz: Iron",
        "workspace_id": 3,
        "thread_id": 55,
        "created_by_id": None,
        "celery_task_id": "deliverable-job:21:attempt:1",
        "attempt_count": 1,
        "request": {
            "version": 1,
            "output": "quiz",
            "prompt": "Create a quiz.",
            "root_thread_id": 55,
        },
    }
    values.update(overrides)
    return SimpleNamespace(**values)


async def test_executor_runs_the_subagent_on_the_job_thread():
    seen = {}

    async def run(**kwargs):
        seen.update(kwargs)
        return SimpleNamespace(
            artifacts=[SimpleNamespace(artifact_id=77, format="quiz")], message="done"
        )

    result = await executor.execute_artifact_deliverable(
        object(), _job(), "llm", run_subagent=run
    )

    assert result.artifact_id == 77
    assert (seen["thread_id"], seen["workspace_id"], seen["prompt"]) == (
        55,
        3,
        "Create a quiz.",
    )


async def test_a_run_without_an_artifact_fails_as_unverified():
    async def run(**_kwargs):
        return SimpleNamespace(artifacts=[], message="I could not do it")

    with pytest.raises(executor.NoArtifactProducedError) as exc:
        await executor.execute_artifact_deliverable(
            object(), _job(), "llm", run_subagent=run
        )
    code, transient = tasks.classify_deliverable_failure(exc.value)
    assert code.value == "verification_failed" and transient is False


async def test_the_request_must_name_the_job_thread():
    bad = _job(request={**_job().request, "root_thread_id": 99})
    with pytest.raises(ValueError):
        await executor.execute_artifact_deliverable(object(), bad, "llm")


async def test_worker_completes_artifact_jobs_and_frees_the_thread_sandbox(
    monkeypatch,
):
    class Session:
        async def commit(self):
            pass

        async def rollback(self):
            pass

    session = Session()
    job = _job()
    terminated = []

    @asynccontextmanager
    async def maker():
        yield session

    async def claim(*_a, **_k):
        return job

    async def resolve_model(*_a, **_k):
        return "llm", SimpleNamespace(is_premium=False)

    heartbeats = []

    async def heartbeat(*_a, **kwargs):
        heartbeats.append((kwargs["phase"], kwargs["progress"]))
        return job

    reporters = []

    async def execute(session_arg, job_arg, llm, *, report_progress):
        assert (session_arg, job_arg, llm) == (session, job, "llm")
        reporters.append(report_progress)
        return SimpleNamespace(artifact_id=88)

    async def complete(*_a, **kwargs):
        assert kwargs["artifact_id"] == 88
        return job

    class Registry:
        async def terminate(self, owner):
            terminated.append(owner)

    async def registry():
        return Registry()

    monkeypatch.setattr(tasks, "get_celery_session_maker", lambda: maker)
    monkeypatch.setattr(tasks, "claim_deliverable_job", claim)
    monkeypatch.setattr(tasks, "_resolve_worker_model", resolve_model)
    monkeypatch.setattr(tasks, "heartbeat_deliverable_job", heartbeat)
    monkeypatch.setattr(tasks, "execute_artifact_deliverable", execute)
    monkeypatch.setattr(tasks, "complete_deliverable_job", complete)
    monkeypatch.setattr(tasks, "get_registry", registry)

    result = await tasks._execute_queued_deliverable(
        21, task_id=job.celery_task_id, retry_provider_failure=False
    )

    assert result == {"status": "ready", "job_id": 21, "artifact_id": 88}
    assert terminated == ["55"]
    # The claim's first word is the first real phase, and the run reports on.
    assert heartbeats == [("preparing", 5)]
    assert isinstance(reporters[0], JobProgressHeartbeat)


async def test_executor_hands_a_chosen_infographic_style_to_the_run():
    seen = {}

    async def run(**kwargs):
        seen.update(kwargs)
        return SimpleNamespace(
            artifacts=[SimpleNamespace(artifact_id=5, format="infographic")], message=""
        )

    job = _job(
        request={
            "version": 1,
            "output": "infographic",
            "prompt": "Create an infographic.",
            "root_thread_id": 55,
            "infographic_style": "anime",
        }
    )
    await executor.execute_artifact_deliverable(object(), job, "llm", run_subagent=run)
    assert seen["infographic_style"] == "anime"


async def test_older_queued_requests_without_a_style_still_run():
    seen = {}

    async def run(**kwargs):
        seen.update(kwargs)
        return SimpleNamespace(
            artifacts=[SimpleNamespace(artifact_id=5, format="quiz")], message=""
        )

    await executor.execute_artifact_deliverable(
        object(), _job(), "llm", run_subagent=run
    )
    assert seen["infographic_style"] is None


async def test_a_required_fragment_is_placed_before_the_run_and_ensured_after(
    monkeypatch,
):
    events = []
    sandbox = SimpleNamespace()

    async def write_file(path, data):
        events.append(("write", path, data))

    sandbox.write_file = write_file

    async def get_sandbox():
        return sandbox

    async def run(**_kwargs):
        events.append(("run",))
        return SimpleNamespace(
            artifacts=[SimpleNamespace(artifact_id=9, format="pdf")], message=""
        )

    async def ensure(_session, job, artifact_id, fragment):
        events.append(("ensure", artifact_id, fragment.marker))
        return True

    monkeypatch.setattr(executor, "ensure_fragment", ensure)
    job = _job(
        request={
            "version": 1,
            "output": "report",
            "prompt": "Create a report.",
            "root_thread_id": 55,
            "required_fragment": {
                "path": "/workspace/studio-embeds.html",
                "html": "<section data-x>embeds</section>",
                "marker": "data-x",
            },
        }
    )

    result = await executor.execute_artifact_deliverable(
        object(), job, "llm", run_subagent=run, get_sandbox=get_sandbox
    )

    assert result.artifact_id == 9
    assert events == [
        ("write", "/workspace/studio-embeds.html", b"<section data-x>embeds</section>"),
        ("run",),
        ("ensure", 9, "data-x"),
    ]


async def test_a_failed_fragment_repair_does_not_fail_the_report(monkeypatch):
    session = SimpleNamespace(rollback_called=False)

    async def rollback():
        session.rollback_called = True

    session.rollback = rollback

    async def run(**_kwargs):
        return SimpleNamespace(
            artifacts=[SimpleNamespace(artifact_id=9, format="pdf")], message=""
        )

    async def ensure(*_args):
        raise RuntimeError("storage down")

    async def get_sandbox():
        async def write_file(*_args):
            return None

        return SimpleNamespace(write_file=write_file)

    monkeypatch.setattr(executor, "ensure_fragment", ensure)
    job = _job(
        request={
            "version": 1,
            "output": "report",
            "prompt": "Create a report.",
            "root_thread_id": 55,
            "required_fragment": {
                "path": "/workspace/studio-embeds.html",
                "html": "<p>x</p>",
                "marker": "data-x",
            },
        }
    )
    result = await executor.execute_artifact_deliverable(
        session, job, "llm", run_subagent=run, get_sandbox=get_sandbox
    )
    assert result.artifact_id == 9 and session.rollback_called


def test_fragment_paths_stay_inside_the_workspace():
    from pydantic import ValidationError

    from app.deliverables.artifact.required_fragment import RequiredFragment

    with pytest.raises(ValidationError):
        RequiredFragment(path="/etc/passwd.html", html="x", marker="data-x")
    with pytest.raises(ValidationError):
        RequiredFragment(path="/workspace/../x.html", html="x", marker="data-x")


def _run_saving(*artifacts):
    async def run(**_kwargs):
        return SimpleNamespace(
            artifacts=[
                SimpleNamespace(artifact_id=artifact_id, format=fmt)
                for artifact_id, fmt in artifacts
            ],
            message="",
        )

    return run


def _job_for(output: str) -> SimpleNamespace:
    return _job(request={**_job().request, "output": output})


async def test_an_artifact_of_the_wrong_kind_does_not_make_the_job_ready():
    with pytest.raises(executor.NoArtifactProducedError) as exc:
        await executor.execute_artifact_deliverable(
            object(),
            _job_for("mindmap"),
            "llm",
            run_subagent=_run_saving((4, "pdf")),
        )
    assert "mindmap" in str(exc.value)
    code, transient = tasks.classify_deliverable_failure(exc.value)
    assert code.value == "verification_failed" and transient is False


async def test_the_first_artifact_of_the_requested_kind_is_the_result():
    result = await executor.execute_artifact_deliverable(
        object(),
        _job_for("slides"),
        "llm",
        run_subagent=_run_saving((1, "png"), (2, "pptx"), (3, "pptx")),
    )
    assert result.artifact_id == 2


@pytest.mark.parametrize(
    ("output", "fmt"),
    [
        ("report", "pdf"),
        ("report", "docx"),
        ("report", "html"),
        ("slides", "pptx"),
        ("data_table", "xlsx"),
        ("quiz", "quiz"),
        ("flashcards", "flashcards"),
        ("mindmap", "mindmap"),
        ("infographic", "infographic"),
    ],
)
async def test_each_output_accepts_its_own_formats(output, fmt):
    result = await executor.execute_artifact_deliverable(
        object(), _job_for(output), "llm", run_subagent=_run_saving((6, fmt))
    )
    assert result.artifact_id == 6


def test_every_requestable_output_has_allowed_formats():
    from typing import get_args

    from app.deliverables.artifact.output_formats import ALLOWED_FORMATS
    from app.deliverables.artifact.request import ArtifactOutput

    assert set(ALLOWED_FORMATS) == set(get_args(ArtifactOutput))


async def test_executor_hands_the_dialog_canvas_and_detail_to_the_run():
    seen = {}

    async def run(**kwargs):
        seen.update(kwargs)
        return SimpleNamespace(
            artifacts=[SimpleNamespace(artifact_id=5, format="infographic")], message=""
        )

    job = _job(
        request={
            "version": 1,
            "output": "infographic",
            "prompt": "Create an infographic.",
            "root_thread_id": 55,
            "infographic_style": "bento-grid",
            "infographic_orientation": "portrait",
            "infographic_detail": "detailed",
            "infographic_description": "Blue theme",
        }
    )
    await executor.execute_artifact_deliverable(object(), job, "llm", run_subagent=run)
    layout = seen["infographic_layout"]
    assert (layout.orientation, layout.detail, layout.description) == (
        "portrait",
        "detailed",
        "Blue theme",
    )


async def test_a_run_without_canvas_choices_gets_no_layout():
    seen = {}

    async def run(**kwargs):
        seen.update(kwargs)
        return SimpleNamespace(
            artifacts=[SimpleNamespace(artifact_id=5, format="quiz")], message=""
        )

    await executor.execute_artifact_deliverable(
        object(), _job(), "llm", run_subagent=run
    )
    assert "infographic_layout" not in seen
