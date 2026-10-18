"""Which mailbox a fresh Microsoft grant is for, from its id_token.

The id_token comes straight from Microsoft's token endpoint, over TLS, in
exchange for this server's client secret, so its signature is not checked
again (OpenID Connect Core 3.1.3.7 allows that for a token received this
way); its audience must still be this app. It is read once at connect time
and never stored.
"""

from __future__ import annotations

from dataclasses import dataclass

import jwt


class UnknownMailboxError(Exception):
    """The id_token names no usable mailbox address."""


@dataclass(frozen=True)
class Mailbox:
    #: The IMAP user name: the address XOAUTH2 signs in as.
    address: str
    #: Stable id of the Microsoft account (tenant + subject): a reconnect
    #: updates the same mailbox even if its address changed.
    subject: str


def _address(value: object) -> str | None:
    if isinstance(value, str) and "@" in value and not any(c.isspace() for c in value):
        return value.strip()
    return None


def mailbox_from_id_token(id_token: str | None, client_id: str) -> Mailbox:
    if not id_token:
        raise UnknownMailboxError("Microsoft sent no id_token")
    try:
        claims = jwt.decode(id_token, options={"verify_signature": False})
    except jwt.PyJWTError as exc:
        raise UnknownMailboxError("Microsoft's id_token could not be read") from exc
    audience = claims.get("aud")
    audiences = audience if isinstance(audience, list) else [audience]
    if client_id not in audiences:
        raise UnknownMailboxError("the id_token was issued to another app")
    # ``email`` is the mailbox's address where Microsoft has one; for work
    # accounts ``preferred_username`` is the sign-in name, which Exchange
    # also accepts. Microsoft sends ``preferred_username`` and ``oid`` only
    # because ``profile`` is among settings.SCOPES; ``sub`` (per-app, also
    # stable) stands in if ``oid`` is ever missing.
    address = _address(claims.get("email")) or _address(
        claims.get("preferred_username")
    )
    if address is None:
        raise UnknownMailboxError("the id_token carries no email address")
    subject = (
        f"{claims.get('tid') or ''}:{claims.get('oid') or claims.get('sub') or ''}"
    )
    return Mailbox(address=address, subject=subject)
