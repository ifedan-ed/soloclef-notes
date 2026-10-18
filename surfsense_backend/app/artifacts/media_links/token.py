"""The signed claim a media link carries: who may play which file, until when.

An interactive report is shown in an iframe sandboxed to an opaque origin, so
the player inside it sends neither the session cookie (SameSite) nor a bearer
header. The viewer therefore swaps each media placeholder for a URL carrying
this claim. It names one file of one artifact in one workspace for one user,
expires within hours, and is re-authorized against that user's current
workspace permissions every time it is used.
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import json
import time
from dataclasses import dataclass

LINK_TTL_SECONDS = 12 * 60 * 60
_DOMAIN = b"surfsense-artifact-media-link:v1:"


@dataclass(frozen=True)
class MediaLinkClaim:
    user_id: str
    workspace_id: int
    artifact_id: int
    file_id: int
    expires_at: int


def _sign(payload: bytes, secret_key: str) -> str:
    if not secret_key:
        raise ValueError("SECRET_KEY is required for media links")
    digest = hmac.new(secret_key.encode(), _DOMAIN + payload, hashlib.sha256)
    return base64.urlsafe_b64encode(digest.digest()).decode().rstrip("=")


def issue_media_token(claim: MediaLinkClaim, *, secret_key: str) -> str:
    payload = json.dumps(
        {
            "u": claim.user_id,
            "w": claim.workspace_id,
            "a": claim.artifact_id,
            "f": claim.file_id,
            "e": claim.expires_at,
        },
        separators=(",", ":"),
        sort_keys=True,
    ).encode()
    encoded = base64.urlsafe_b64encode(payload).decode().rstrip("=")
    return f"{encoded}.{_sign(payload, secret_key)}"


def read_media_token(
    token: str, *, secret_key: str, now: int | None = None
) -> MediaLinkClaim | None:
    """The claim, or ``None`` for anything forged, malformed or expired."""
    try:
        encoded, signature = token.split(".", 1)
        payload = base64.urlsafe_b64decode(encoded + "=" * (-len(encoded) % 4))
        if not hmac.compare_digest(signature, _sign(payload, secret_key)):
            return None
        data = json.loads(payload)
        claim = MediaLinkClaim(
            user_id=str(data["u"]),
            workspace_id=int(data["w"]),
            artifact_id=int(data["a"]),
            file_id=int(data["f"]),
            expires_at=int(data["e"]),
        )
    except (ValueError, KeyError, TypeError):
        return None
    if claim.expires_at < (int(time.time()) if now is None else now):
        return None
    return claim


def new_claim(
    *, user_id: str, workspace_id: int, artifact_id: int, file_id: int
) -> MediaLinkClaim:
    return MediaLinkClaim(
        user_id=user_id,
        workspace_id=workspace_id,
        artifact_id=artifact_id,
        file_id=file_id,
        expires_at=int(time.time()) + LINK_TTL_SECONDS,
    )
