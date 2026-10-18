"""The grant rule on its own: ownership is never granted, nothing above you is."""

from __future__ import annotations

import uuid
from types import SimpleNamespace

import pytest

from app.workspace_roles.grants import (
    can_grant_role,
    can_manage_member,
    role_permissions_allowed,
)

pytestmark = pytest.mark.unit

OWNER_ROLE = SimpleNamespace(permissions=["*"])
EDITOR_ROLE = SimpleNamespace(permissions=["documents:read", "members:invite"])
VIEWER_ROLE = SimpleNamespace(permissions=["documents:read"])


def member(role, *, is_owner=False):
    return SimpleNamespace(user_id=uuid.uuid4(), is_owner=is_owner, role=role)


def test_nobody_grants_the_wildcard():
    assert not can_grant_role(member(OWNER_ROLE, is_owner=True), OWNER_ROLE)
    assert not can_grant_role(member(EDITOR_ROLE), OWNER_ROLE)


def test_grants_stop_at_your_own_permissions():
    editor = member(EDITOR_ROLE)
    assert can_grant_role(editor, EDITOR_ROLE)
    assert can_grant_role(editor, VIEWER_ROLE)
    assert not can_grant_role(member(VIEWER_ROLE), EDITOR_ROLE)
    assert not can_grant_role(member(None), VIEWER_ROLE)


def test_members_above_you_the_owner_and_yourself_are_off_limits():
    editor = member(EDITOR_ROLE)
    assert can_manage_member(editor, member(VIEWER_ROLE))
    assert not can_manage_member(member(VIEWER_ROLE), editor)
    assert not can_manage_member(editor, member(OWNER_ROLE, is_owner=True))
    assert not can_manage_member(editor, editor)


def test_only_owners_write_roles_and_never_the_wildcard():
    owner = member(OWNER_ROLE, is_owner=True)
    assert role_permissions_allowed(owner, ["documents:read"])
    assert not role_permissions_allowed(owner, ["*"])
    assert not role_permissions_allowed(member(EDITOR_ROLE), ["documents:read"])
