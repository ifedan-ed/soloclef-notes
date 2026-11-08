"""Reading the provider's discovery document and keys."""

from __future__ import annotations

import httpx
import pytest

from app.auth_oidc.discovery import CACHE_SECONDS, ProviderDirectory, parse_metadata
from app.auth_oidc.errors import PROVIDER_UNAVAILABLE, OidcLoginError
from tests.utils.fake_oidc_provider import DISCOVERY_URL, ISSUER, FakeOidcProvider

pytestmark = pytest.mark.unit


def test_a_document_for_another_issuer_is_refused():
    document = FakeOidcProvider(issuer="https://evil.test/o/notes/")._discovery()
    with pytest.raises(OidcLoginError) as refused:
        parse_metadata(document, DISCOVERY_URL)
    assert refused.value.code == PROVIDER_UNAVAILABLE


def test_a_document_missing_an_endpoint_is_refused():
    document = FakeOidcProvider()._discovery()
    del document["token_endpoint"]
    with pytest.raises(OidcLoginError):
        parse_metadata(document, DISCOVERY_URL)


async def test_reads_the_provider_once_then_keeps_it():
    provider = FakeOidcProvider()
    now = [0.0]
    directory = ProviderDirectory(
        DISCOVERY_URL, transport=provider.transport, clock=lambda: now[0]
    )

    first = await directory.metadata()
    await directory.metadata()
    await directory.signing_keys()
    await directory.signing_keys()
    assert first.issuer == ISSUER
    assert (provider.discovery_requests, provider.jwks_requests) == (1, 1)

    now[0] = CACHE_SECONDS + 1
    await directory.signing_keys()
    assert (provider.discovery_requests, provider.jwks_requests) == (2, 2)


async def test_an_unreachable_provider_is_reported_not_raised_raw():
    def down(request: httpx.Request) -> httpx.Response:
        raise httpx.ConnectError("refused", request=request)

    directory = ProviderDirectory(DISCOVERY_URL, transport=httpx.MockTransport(down))
    with pytest.raises(OidcLoginError) as refused:
        await directory.metadata()
    assert refused.value.code == PROVIDER_UNAVAILABLE
