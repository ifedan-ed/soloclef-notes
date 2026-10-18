"""``RegenerateRequest`` accepts ``from_message_id`` + ``revert_actions``.

A validator prevents callers from requesting a revert pass without specifying
which turn to roll back. Where the re-run starts is covered by
``tests/unit/tasks/chat/streaming/flows/regenerate/test_rewind_point.py``.
"""

from __future__ import annotations

import pytest

from app.schemas.new_chat import RegenerateRequest


class TestRegenerateRequestValidation:
    def test_revert_actions_requires_from_message_id(self) -> None:
        with pytest.raises(Exception) as exc:
            RegenerateRequest(
                workspace_id=1,
                user_query="hi",
                revert_actions=True,
            )
        msg = str(exc.value).lower()
        assert "from_message_id" in msg

    def test_from_message_id_without_revert_is_allowed(self) -> None:
        req = RegenerateRequest(
            workspace_id=1,
            user_query="hi",
            from_message_id=42,
        )
        assert req.from_message_id == 42
        assert req.revert_actions is False

    def test_revert_actions_with_from_message_id_passes(self) -> None:
        req = RegenerateRequest(
            workspace_id=1,
            user_query="hi",
            from_message_id=42,
            revert_actions=True,
        )
        assert req.revert_actions is True
