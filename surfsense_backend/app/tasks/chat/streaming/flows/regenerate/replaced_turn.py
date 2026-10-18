"""Which saved messages a Regenerate or an Edit replaces.

A turn is replaced from its question on: the question, its answer and every
message after them (Edit can start at an earlier question). The page names
the message (``from_message_id``); an answer's id stands for its turn's
question. Without one it is the latest question. The question's saved text and
images are what Regenerate asks again.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.db import NewChatMessage, NewChatMessageRole
from app.utils.user_message_multimodal import split_persisted_user_content_parts


class FromMessageNotInThreadError(LookupError):
    """``from_message_id`` names no message of this thread."""


@dataclass(frozen=True)
class ReplacedTurn:
    user_message_id: int
    turn_id: str | None
    """The question's turn; None on rows saved before turn ids existed."""
    starts_at: datetime
    """When the question was saved; every row from then on is replaced."""
    user_query: str
    image_urls: list[str]
    message_ids: list[int]
    turn_ids: list[str]
    """The turns of the replaced rows, oldest first."""
    is_latest_turn: bool
    """No question was asked after this one."""


async def find_replaced_turn(
    session: AsyncSession, *, thread_id: int, from_message_id: int | None
) -> ReplacedTurn | None:
    """The turn to replace, or None when the chat has no question to ask again."""
    if from_message_id is None:
        question = await _latest_question(session, thread_id)
    else:
        named = (
            await session.execute(
                select(NewChatMessage).where(
                    NewChatMessage.id == from_message_id,
                    NewChatMessage.thread_id == thread_id,
                )
            )
        ).scalar_one_or_none()
        if named is None:
            raise FromMessageNotInThreadError(from_message_id)
        question = (
            named
            if named.role == NewChatMessageRole.USER
            else await _question_of(session, thread_id, named)
        )
    if question is None:
        return None

    rows = (
        await session.execute(
            select(NewChatMessage.id, NewChatMessage.role, NewChatMessage.turn_id)
            .where(
                NewChatMessage.thread_id == thread_id,
                NewChatMessage.created_at >= question.created_at,
            )
            .order_by(NewChatMessage.created_at, NewChatMessage.id)
        )
    ).all()
    turn_ids: list[str] = []
    for row in rows:
        if row.turn_id and row.turn_id not in turn_ids:
            turn_ids.append(row.turn_id)
    user_query, image_urls = _question_content(question.content)
    return ReplacedTurn(
        user_message_id=question.id,
        turn_id=question.turn_id,
        starts_at=question.created_at,
        user_query=user_query,
        image_urls=image_urls,
        message_ids=[row.id for row in rows],
        turn_ids=turn_ids,
        is_latest_turn=sum(1 for row in rows if row.role == NewChatMessageRole.USER)
        == 1,
    )


async def _latest_question(
    session: AsyncSession, thread_id: int
) -> NewChatMessage | None:
    return (
        await session.execute(
            select(NewChatMessage)
            .where(
                NewChatMessage.thread_id == thread_id,
                NewChatMessage.role == NewChatMessageRole.USER,
            )
            .order_by(NewChatMessage.created_at.desc(), NewChatMessage.id.desc())
            .limit(1)
        )
    ).scalar_one_or_none()


async def _question_of(
    session: AsyncSession, thread_id: int, answer: NewChatMessage
) -> NewChatMessage | None:
    """The question an answer belongs to: same turn, else the one before it."""
    if answer.turn_id:
        same_turn = (
            await session.execute(
                select(NewChatMessage).where(
                    NewChatMessage.thread_id == thread_id,
                    NewChatMessage.turn_id == answer.turn_id,
                    NewChatMessage.role == NewChatMessageRole.USER,
                )
            )
        ).scalar_one_or_none()
        if same_turn is not None:
            return same_turn
    return (
        await session.execute(
            select(NewChatMessage)
            .where(
                NewChatMessage.thread_id == thread_id,
                NewChatMessage.role == NewChatMessageRole.USER,
                NewChatMessage.created_at <= answer.created_at,
            )
            .order_by(NewChatMessage.created_at.desc(), NewChatMessage.id.desc())
            .limit(1)
        )
    ).scalar_one_or_none()


def _question_content(content: object) -> tuple[str, list[str]]:
    if isinstance(content, str):
        return content, []
    if isinstance(content, list):
        return split_persisted_user_content_parts(content)
    return "", []
