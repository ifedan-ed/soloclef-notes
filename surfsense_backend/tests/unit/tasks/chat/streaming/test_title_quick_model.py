"""Thread titles run on the quick-tasks model, not the turn's chat model."""

from __future__ import annotations

import contextlib

import pytest

from app.tasks.chat.streaming.flows.new_chat import title_gen

pytestmark = pytest.mark.unit


class _Quick:
    model = "openai/ds-deepseek-v4.1-flash"
    model_kwargs = {"extra_body": {"thinking": {"type": "disabled"}}}


@contextlib.asynccontextmanager
async def _session():
    yield object()


async def test_a_title_uses_the_quick_model_with_thinking_off(monkeypatch) -> None:
    async def quick_llm(_session, workspace_id):
        assert workspace_id == 4
        return _Quick()

    monkeypatch.setattr(title_gen, "shielded_async_session", _session)
    monkeypatch.setattr("app.quick_tasks.llm.quick_llm", quick_llm)

    llm, extra = await title_gen._title_llm(4, chat_llm="chat")

    assert isinstance(llm, _Quick)
    assert extra == {"extra_body": {"thinking": {"type": "disabled"}}}


@pytest.mark.parametrize("failure", [None, RuntimeError("db down")])
async def test_without_a_quick_model_the_title_uses_the_chat_model(
    monkeypatch, failure
) -> None:
    async def quick_llm(_session, _workspace_id):
        if failure:
            raise failure
        return None

    monkeypatch.setattr(title_gen, "shielded_async_session", _session)
    monkeypatch.setattr("app.quick_tasks.llm.quick_llm", quick_llm)

    assert await title_gen._title_llm(4, chat_llm="chat") == ("chat", {})


class _FirstResponseSession:
    """The first-response probe finds no earlier assistant message."""

    async def execute(self, _query):
        class _Result:
            def scalars(self):
                return self

            def first(self):
                return None

        return _Result()


@contextlib.asynccontextmanager
async def _probe_session():
    yield _FirstResponseSession()


def _completion(text):
    class _Message:
        content = text

    class _Choice:
        message = _Message()

    class _Response:
        choices = [_Choice()]
        usage = None
        model = "m"

    return _Response()


async def test_a_title_names_the_language_and_retries_a_chinese_reply(monkeypatch):
    replies = iter(["P值的十二个误解", "Twelve P Value Misconceptions"])
    prompts: list[str] = []

    async def acompletion(*, messages, **_kwargs):
        prompts.append(messages[-1]["content"])
        return _completion(next(replies))

    async def title_llm(_workspace_id, llm):
        return _Quick(), {}

    monkeypatch.setattr(title_gen, "shielded_async_session", _probe_session)
    monkeypatch.setattr(title_gen, "_title_llm", title_llm)
    monkeypatch.setattr("litellm.acompletion", acompletion)

    title, _usage = await title_gen._generate_title(
        chat_id=1,
        workspace_id=4,
        user_query="What are the common misconceptions about P values?",
        user_image_data_urls=None,
        assistant_message_id=9,
        llm=None,
        agent_config=None,
    )

    assert title == "Twelve P Value Misconceptions"
    assert "Write your entire reply in English" in prompts[0]
    assert "not in English" in prompts[1]
