"""The rule every invite, role change and removal goes through.

Nobody hands out ownership: a role carrying the full-access wildcard is never
granted through an invite or a role change (the workspace creator is its
Owner). Anyone else may only grant a role whose permissions they hold
themselves, and may only act on members whose current role is at or below
their own, never on themselves or the Owner.
"""

from __future__ import annotations

from collections.abc import Iterable
from typing import Protocol

from app.db import Permission

FULL_ACCESS = Permission.FULL_ACCESS.value


class _Role(Protocol):
    permissions: list[str] | None


class _Membership(Protocol):
    user_id: object
    is_owner: bool
    role: _Role | None


def grants_full_access(role: _Role | None) -> bool:
    return role is not None and FULL_ACCESS in (role.permissions or [])


def held_permissions(membership: _Membership) -> set[str]:
    if membership.is_owner:
        return {FULL_ACCESS}
    if membership.role is None:
        return set()
    return set(membership.role.permissions or [])


def _covers(held: set[str], wanted: Iterable[str]) -> bool:
    if FULL_ACCESS in held:
        return True
    return set(wanted) <= held


def can_grant_role(actor: _Membership, role: _Role) -> bool:
    """Whether ``actor`` may give ``role`` to someone through an invite or a change."""
    if grants_full_access(role):
        return False
    return _covers(held_permissions(actor), role.permissions or [])


def can_manage_member(actor: _Membership, target: _Membership) -> bool:
    """Whether ``actor`` may change the role of, or remove, ``target``."""
    if target.is_owner or target.user_id == actor.user_id:
        return False
    if target.role is None:
        return True
    if grants_full_access(target.role):
        return actor.is_owner
    return _covers(held_permissions(actor), target.role.permissions or [])


def role_permissions_allowed(actor: _Membership, permissions: Iterable[str]) -> bool:
    """Whether a custom role with these permissions may be written by ``actor``.

    Only Owners edit roles, and no role but the seeded Owner carries the wildcard.
    """
    wanted = list(permissions)
    return actor.is_owner and FULL_ACCESS not in wanted
