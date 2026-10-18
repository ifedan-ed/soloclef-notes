"""Request-scoped caller identity.

Over streamable-http one process serves many users, so the caller's key (an
API key, or an agent's sign-in token) lives in a contextvar for the life of a
request: the auth middleware binds it and the client reads it when calling the
backend, which checks it again on every call. Under stdio there is no request,
so the contextvar is empty and the env key is used instead.
"""

from __future__ import annotations

from contextvars import ContextVar, Token

_LOCAL_IDENTITY = "__local__"

_api_key: ContextVar[str | None] = ContextVar("surfsense_api_key", default=None)
# Who the caller is, for per-caller state. An API key is its own identity; an
# agent's sign-in token is replaced every hour when it refreshes, so it is
# bound with the backend's stable ``user:<id>`` instead.
_identity: ContextVar[str | None] = ContextVar("surfsense_identity", default=None)


BoundCaller = tuple[Token, Token]


def bind_api_key(api_key: str | None, identity: str | None = None) -> BoundCaller:
    """Bind the caller's credential (and identity) to the current request;
    returns a reset token."""
    return _api_key.set(api_key), _identity.set(identity)


def unbind_api_key(token: BoundCaller) -> None:
    key_token, identity_token = token
    _identity.reset(identity_token)
    _api_key.reset(key_token)


def current_api_key() -> str | None:
    """The caller's key for the in-flight request, or ``None`` under stdio."""
    return _api_key.get()


def current_identity() -> str:
    """Stable per-caller key for scoping request state; shared under stdio."""
    return _identity.get() or _api_key.get() or _LOCAL_IDENTITY
