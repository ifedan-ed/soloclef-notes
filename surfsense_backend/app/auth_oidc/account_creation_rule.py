"""Whether a first single sign-on may create a Notes account.

Two settings open it, each documented in ``custom/README.md``:

* ``OIDC_AUTOCREATE_DOMAINS`` (e.g. ``example.com``): addresses at these
  domains get an account on their first sign-in. The domain must match
  exactly, so ``sub.example.com``, ``evil-example.com`` and
  ``example.com.example`` do not count.
* ``OIDC_ALLOW_SIGNUP=TRUE``: any address gets one, whatever its domain.

Who may reach Notes at all is Authentik's decision, made before Notes ever
sees a token: the application's policy bindings (``notes-users``, and
``example-accounts-only``, formerly ``soloclef-accounts-only``, which lets in
@example.com but never the App Review account). Notes does not read group
names, so renaming a group in Authentik cannot quietly change who gets an
account here.
"""

from __future__ import annotations

from collections.abc import Collection


def email_domain(email: str) -> str:
    """The part after the only "@", lowercased; "" for anything malformed."""
    local, at, domain = email.strip().lower().rpartition("@")
    if not at or not local or "@" in local:
        return ""
    return domain


def may_create_account(
    email: str, *, allow_signup: bool, autocreate_domains: Collection[str]
) -> bool:
    domain = email_domain(email)
    if not domain:
        return False
    return allow_signup or domain in autocreate_domains


def describe(*, allow_signup: bool, autocreate_domains: Collection[str]) -> str:
    """The rule in words, for the startup log."""
    if allow_signup:
        return "any verified address"
    if autocreate_domains:
        return "verified addresses at " + ", ".join(sorted(autocreate_domains))
    return "nobody (existing accounts only)"
