"""Which saved messages a Regenerate or an Edit replaces."""

from __future__ import annotations

import pytest

from app.db import NewChatMessage, NewChatMessageRole
from app.tasks.chat.streaming.flows.regenerate import (
    FromMessageNotInThreadError,
    find_replaced_turn,
)

pytestmark = pytest.mark.integration


async def test_without_a_message_it_is_the_latest_question(db_session, chat, save_turn):
    await save_turn(chat, "What is an EVD?", "A drain.", turn_id=f"{chat.id}:1")
    q2, a2 = await save_turn(
        chat, "EVD replacing output?", "Replace losses.", turn_id=f"{chat.id}:2"
    )

    replaced = await find_replaced_turn(
        db_session, thread_id=chat.id, from_message_id=None
    )

    assert replaced.user_message_id == q2.id
    assert replaced.user_query == "EVD replacing output?"
    assert replaced.message_ids == [q2.id, a2.id]
    assert replaced.turn_ids == [f"{chat.id}:2"]
    assert replaced.is_latest_turn is True


async def test_an_answer_stands_for_its_question(db_session, chat, save_turn):
    q1, a1 = await save_turn(
        chat, "What is an EVD?", "A drain.", turn_id=f"{chat.id}:1"
    )
    q2, a2 = await save_turn(chat, "Output?", "Losses.", turn_id=f"{chat.id}:2")

    replaced = await find_replaced_turn(
        db_session, thread_id=chat.id, from_message_id=a1.id
    )

    assert replaced.user_message_id == q1.id
    assert replaced.message_ids == [q1.id, a1.id, q2.id, a2.id]
    assert replaced.turn_ids == [f"{chat.id}:1", f"{chat.id}:2"]
    assert replaced.is_latest_turn is False


async def test_an_answer_saved_before_turn_ids_finds_the_question_before_it(
    db_session, chat, save_turn
):
    q1, a1 = await save_turn(chat, "What is an EVD?", "A drain.", turn_id=None)

    replaced = await find_replaced_turn(
        db_session, thread_id=chat.id, from_message_id=a1.id
    )

    assert replaced.user_message_id == q1.id
    assert replaced.turn_id is None
    assert replaced.turn_ids == []


async def test_the_saved_images_are_asked_again(db_session, chat, save_turn):
    question, _answer = await save_turn(chat, "", "A drain.", turn_id=f"{chat.id}:1")
    question.content = [
        {"type": "text", "text": "What is this?"},
        {"type": "image", "image": "data:image/png;base64,AAAA"},
    ]
    await db_session.flush()

    replaced = await find_replaced_turn(
        db_session, thread_id=chat.id, from_message_id=question.id
    )

    assert replaced.user_query == "What is this?"
    assert replaced.image_urls == ["data:image/png;base64,AAAA"]


async def test_a_message_of_another_chat_is_refused(db_session, chat, save_turn):
    with pytest.raises(FromMessageNotInThreadError):
        await find_replaced_turn(
            db_session, thread_id=chat.id, from_message_id=987_654_321
        )


async def test_a_chat_without_a_question_has_nothing_to_replace(db_session, chat):
    db_session.add(
        NewChatMessage(
            thread_id=chat.id,
            role=NewChatMessageRole.ASSISTANT,
            content=[{"type": "text", "text": "Welcome"}],
        )
    )
    await db_session.flush()

    assert (
        await find_replaced_turn(db_session, thread_id=chat.id, from_message_id=None)
        is None
    )
