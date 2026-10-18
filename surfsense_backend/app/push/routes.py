"""The phone's side of push: its token, and its choices.

    POST   /api/v1/push/devices                 register this phone (upsert by token, bound to its sign-in)
    DELETE /api/v1/push/devices {token}         forget this phone (sign-out)
    GET    /api/v1/push/preferences             every kind with its choice
    PUT    /api/v1/push/preferences             change some choices

Session only (no API keys): a token names a phone that shows this account.
A phone registers with its sign-in (``session_id``, required) and takes
pushes only while that sign-in is live; revoking it deletes the row
(app/utils/refresh_tokens.py). A registration naming a sign-in that is not
this account's live one is refused with 401 and registers nothing, so a phone
the server signed out cannot come back unbound on an access token that has
not run out yet (``ACCESS_TOKEN_LIFETIME_SECONDS``); the app then takes its
signed-out path.

``DELETE`` takes the token in its JSON body, which proxies do not log; the
``?token=`` query of the first app builds is still read when there is no body
(the backend's own access log masks it, ``observability/logs/redaction.py``).
"""

from __future__ import annotations

from fastapi import APIRouter, Body, Depends, HTTPException, Query, status

from app.auth.context import AuthContext
from app.push.kinds import KINDS, merged_choices
from app.push.schemas import (
    DeviceOut,
    DeviceRegistration,
    DeviceRemoval,
    KindOut,
    PreferencesIn,
    PreferencesOut,
    is_expo_token,
)
from app.push.store import DbStore, get_push_store
from app.users import require_session_context

router = APIRouter(prefix="/push", tags=["push"])

#: A registration's sign-in is not this account's live one (401).
SESSION_ENDED = "PUSH_SESSION_ENDED"


def _preferences(saved: dict | None) -> PreferencesOut:
    return PreferencesOut(
        choices=merged_choices(saved),
        kinds=[
            KindOut(kind=k.kind, label=k.label, description=k.description)
            for k in KINDS
        ],
    )


@router.post("/devices", response_model=DeviceOut)
async def register_device(
    registration: DeviceRegistration,
    auth: AuthContext = Depends(require_session_context),
    store: DbStore = Depends(get_push_store),
) -> DeviceOut:
    """This phone takes pushes for this account from now on (and no other).

    Only while its sign-in (``session_id``) is live: one that is not this
    account's live sign-in (signed out from the devices list, everywhere, by
    a password change, or another account's) answers 401 and registers nothing.
    """
    family = registration.session_id
    if not await store.family_is_live(auth.user.id, family):
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail={
                "code": SESSION_ENDED,
                "message": "This sign-in has ended; sign in again",
            },
        )
    device = await store.upsert_device(
        auth.user.id,
        token=registration.token,
        platform=registration.platform,
        label=registration.label,
        language=registration.language,
        session_family_id=family,
    )
    await store.commit()
    return DeviceOut(
        id=device.id,
        token=device.token,
        platform=device.platform,
        label=device.label,
        language=device.language,
        last_seen_at=device.last_seen_at,
    )


@router.delete("/devices", status_code=status.HTTP_204_NO_CONTENT)
async def unregister_device(
    removal: DeviceRemoval | None = Body(default=None),
    token: str | None = Query(
        default=None,
        deprecated=True,
        description="The first app builds' way; send {token} in the body instead",
    ),
    auth: AuthContext = Depends(require_session_context),
    store: DbStore = Depends(get_push_store),
) -> None:
    """Forget this phone. Idempotent: a token not registered answers 204 too."""
    value = (removal.token if removal is not None else token or "").strip()
    if is_expo_token(value):
        await store.remove_device(auth.user.id, value)
        await store.commit()


@router.get("/preferences", response_model=PreferencesOut)
async def get_preferences(
    auth: AuthContext = Depends(require_session_context),
    store: DbStore = Depends(get_push_store),
) -> PreferencesOut:
    return _preferences(await store.choices_of(auth.user.id))


@router.put("/preferences", response_model=PreferencesOut)
async def put_preferences(
    body: PreferencesIn,
    auth: AuthContext = Depends(require_session_context),
    store: DbStore = Depends(get_push_store),
) -> PreferencesOut:
    """Change the kinds given; the rest keep their choice."""
    saved = await store.save_choices(auth.user.id, body.choices)
    await store.commit()
    return _preferences(saved)
