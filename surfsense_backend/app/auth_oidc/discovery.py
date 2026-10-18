"""The provider's published endpoints and signing keys, fetched and kept.

Both are re-read after ``CACHE_SECONDS`` (an hour), and the keys also as soon
as a token names a key id Notes has not seen, so a key rotation in Authentik
needs no restart here.
"""

from __future__ import annotations

import asyncio
import time
from dataclasses import dataclass

import httpx

from app.auth_oidc.errors import PROVIDER_UNAVAILABLE, OidcLoginError

CACHE_SECONDS = 3600
FETCH_TIMEOUT_SECONDS = 10
_WELL_KNOWN = "/.well-known/openid-configuration"


@dataclass(frozen=True)
class ProviderMetadata:
    issuer: str
    authorization_endpoint: str
    token_endpoint: str
    jwks_uri: str
    signing_algorithms: tuple[str, ...]
    #: Where the provider ends its own session (RP-Initiated Logout), if it says.
    end_session_endpoint: str = ""


def parse_metadata(document: dict, discovery_url: str) -> ProviderMetadata:
    """Accept a discovery document only if it describes the provider asked.

    OpenID Connect Discovery 1.0, section 4.3: the issuer must be the URL the
    document was fetched from, minus the well-known suffix. A mismatch means
    the URL points somewhere else than the provider that signs the tokens.
    """
    required = ("issuer", "authorization_endpoint", "token_endpoint", "jwks_uri")
    missing = [key for key in required if not document.get(key)]
    if missing:
        raise OidcLoginError(
            PROVIDER_UNAVAILABLE, f"discovery document lacks {', '.join(missing)}"
        )
    expected = discovery_url.removesuffix(_WELL_KNOWN).rstrip("/")
    if document["issuer"].rstrip("/") != expected:
        raise OidcLoginError(
            PROVIDER_UNAVAILABLE, "discovery issuer does not match its URL"
        )
    algorithms = tuple(
        document.get("id_token_signing_alg_values_supported") or ("RS256",)
    )
    return ProviderMetadata(
        issuer=document["issuer"],
        authorization_endpoint=document["authorization_endpoint"],
        token_endpoint=document["token_endpoint"],
        jwks_uri=document["jwks_uri"],
        signing_algorithms=algorithms,
        end_session_endpoint=str(document.get("end_session_endpoint") or ""),
    )


class ProviderDirectory:
    """One provider's metadata and key set, shared by every sign-in."""

    def __init__(
        self,
        discovery_url: str,
        *,
        transport: httpx.AsyncBaseTransport | None = None,
        clock=time.monotonic,
    ) -> None:
        self._discovery_url = discovery_url
        self._transport = transport
        self._clock = clock
        self._lock = asyncio.Lock()
        self._metadata: ProviderMetadata | None = None
        self._metadata_at = 0.0
        self._keys: dict | None = None
        self._keys_at = 0.0

    def http_client(self) -> httpx.AsyncClient:
        return httpx.AsyncClient(
            timeout=FETCH_TIMEOUT_SECONDS, transport=self._transport
        )

    async def _get_json(self, url: str) -> dict:
        try:
            async with self.http_client() as client:
                response = await client.get(url, headers={"Accept": "application/json"})
            response.raise_for_status()
            return response.json()
        except (httpx.HTTPError, ValueError) as exc:
            raise OidcLoginError(
                PROVIDER_UNAVAILABLE, f"could not read {url}: {exc}"
            ) from exc

    def _fresh(self, fetched_at: float) -> bool:
        return self._clock() - fetched_at < CACHE_SECONDS

    async def metadata(self) -> ProviderMetadata:
        if self._metadata is not None and self._fresh(self._metadata_at):
            return self._metadata
        async with self._lock:
            if self._metadata is None or not self._fresh(self._metadata_at):
                document = await self._get_json(self._discovery_url)
                self._metadata = parse_metadata(document, self._discovery_url)
                self._metadata_at = self._clock()
        return self._metadata

    async def signing_keys(self, *, refresh: bool = False) -> dict:
        """The provider's JWKS document (``{"keys": [...]}``)."""
        metadata = await self.metadata()
        if not refresh and self._keys is not None and self._fresh(self._keys_at):
            return self._keys
        async with self._lock:
            if refresh or self._keys is None or not self._fresh(self._keys_at):
                self._keys = await self._get_json(metadata.jwks_uri)
                self._keys_at = self._clock()
        return self._keys
