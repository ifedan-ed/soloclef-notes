"""The tokenizer's lock is held only while the tokenizer (or a model in this
process) runs: never while a batch waits for its slot or a hosted model
answers, and never on the event loop."""

from __future__ import annotations

import threading
import time

import pytest

from app.embedding_roles import document_slots
from app.embedding_roles.document_embedding import MODEL_LOCK
from app.utils import document_converters
from app.utils.document_converters import embed_texts

from .conftest import DOCUMENT_NAME

pytestmark = pytest.mark.unit

SLOT = f"{document_slots.SLOT_KEY_PREFIX}:{DOCUMENT_NAME}:0"


def _free_from_another_thread() -> bool:
    got: list[bool] = []

    def take():
        if MODEL_LOCK.acquire(timeout=0.5):
            MODEL_LOCK.release()
            got.append(True)

    thread = threading.Thread(target=take)
    thread.start()
    thread.join()
    return bool(got)


def test_the_lock_is_free_while_a_batch_waits_for_its_slot(roles, monkeypatch):
    monkeypatch.setattr(document_slots, "_POLL_SECONDS", 0.01)
    roles.redis.hold(SLOT)  # another process's batch
    worker = threading.Thread(target=embed_texts, args=(["a chunk"],), daemon=True)
    worker.start()
    try:
        time.sleep(0.1)
        assert roles.document.requests == [], "still waiting for the slot"
        free = _free_from_another_thread()
    finally:
        roles.redis.free(SLOT)
        worker.join(timeout=5)

    assert free, "a question or a tokenizer is not blocked"
    assert roles.document.texts == ["a chunk"]


def test_the_lock_is_free_while_a_hosted_model_answers(roles, monkeypatch):
    free_during_request: list[bool] = []
    answer = roles.document.embed_batch

    def slow_host(texts):
        free_during_request.append(_free_from_another_thread())
        return answer(texts)

    monkeypatch.setattr(roles.document, "embed_batch", slow_host)

    embed_texts(["a chunk"])

    assert free_during_request == [True]


@pytest.mark.parametrize("processor", ["_save", "markdown_processor"])
async def test_a_file_summary_is_cut_off_the_event_loop(roles, monkeypatch, processor):
    import importlib

    module = importlib.import_module(f"app.tasks.document_processors.{processor}")
    loop_thread = threading.current_thread()
    ran_in: list[threading.Thread] = []
    real = document_converters.titled_for_embedding

    def recording(title, text):
        ran_in.append(threading.current_thread())
        return real(title, text)

    async def nothing(*_args, **_kwargs):
        return None

    class TaskLog:
        def __init__(self, *_a, **_k):
            pass

        async def log_task_start(self, **_k):
            return None

        async def log_task_success(self, *_a, **_k):
            return None

    class Session:
        def add(self, _obj):
            pass

        async def commit(self):
            pass

        async def refresh(self, _obj):
            pass

        async def rollback(self):
            pass

    monkeypatch.setattr(module, "titled_for_embedding", recording)
    monkeypatch.setattr(module, "find_existing_document_with_migration", nothing)
    if processor == "_save":
        await module.save_file_document(Session(), "a.md", "text", 1, "u", "DOCLING")
    else:
        monkeypatch.setattr(module, "TaskLoggingService", TaskLog)
        await module.add_received_markdown_file_document(
            Session(), "a.md", "text", 1, "u"
        )

    assert ran_in and ran_in[0] is not loop_thread
