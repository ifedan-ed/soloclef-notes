"""Comparing and showing the address an invite is bound to."""

from __future__ import annotations


def normalize_address(address: str) -> str:
    return address.strip().lower()


def same_address(a: str | None, b: str | None) -> bool:
    if not a or not b:
        return False
    return normalize_address(a) == normalize_address(b)


def mask_address(address: str | None) -> str | None:
    """``a****@example.com``: enough to recognise, not enough to harvest."""
    if not address or "@" not in address:
        return None
    local, domain = address.rsplit("@", 1)
    return f"{local[:1]}{'*' * max(1, min(len(local) - 1, 4))}@{domain}"
