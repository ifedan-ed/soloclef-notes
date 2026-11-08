"""The phone app's sign-in (app/auth_oidc/app_sign_in.py, plan item B1):
the start's checks, the PKCE arithmetic, the one-time codes, what the
attempt cookie carries, and the account chooser."""

from __future__ import annotations

from urllib.parse import parse_qs, urlsplit

import pytest

from app.auth_oidc import app_sign_in
from app.auth_oidc.app_sign_in import (
    BAD_CHALLENGE,
    BAD_REDIRECT,
    CODE_LIFETIME_SECONDS,
    AppGrant,
    AppRequest,
    AppSignInRefused,
    MemoryAppCodes,
    app_return,
    challenge_of,
    parse_app_request,
    verifier_matches,
)
from app.auth_oidc.authorization_request import authorization_url
from app.auth_oidc.discovery import parse_metadata
from app.auth_oidc.login_attempt import LoginAttempt, reopen
from app.auth_oidc.settings import (
    DEFAULT_APP_REDIRECTS,
    OidcSettings,
    load_oidc_settings,
    parse_app_redirects,
)
from tests.utils.fake_oidc_provider import (
    CLIENT_ID,
    DISCOVERY_URL,
    REDIRECT_URI,
    FakeOidcProvider,
)

pytestmark = pytest.mark.unit

SECRET = "unit-test-secret-key-of-reasonable-length"
ALLOWED = frozenset({"soloclef://auth"})
# RFC 7636, appendix B: the verifier and the S256 challenge it gives.
RFC_VERIFIER = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
RFC_CHALLENGE = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"


def good_params(**over):
    params = {
        "app_redirect": "soloclef://auth",
        "challenge": RFC_CHALLENGE,
        "challenge_method": "S256",
        "device": "iPhone 15 Pro · Soloclef app",
    }
    params.update(over)
    return params


# -- the start's checks -----------------------------------------------------


def test_the_apps_own_address_with_an_s256_challenge_is_accepted():
    request = parse_app_request(good_params(), ALLOWED)
    assert request == AppRequest(
        redirect="soloclef://auth",
        challenge=RFC_CHALLENGE,
        device="iPhone 15 Pro · Soloclef app",
        choose_account=False,
    )


def test_use_a_different_address_asks_for_the_account_chooser():
    assert parse_app_request(good_params(choose_account="1"), ALLOWED).choose_account
    assert not parse_app_request(
        good_params(choose_account="0"), ALLOWED
    ).choose_account


@pytest.mark.parametrize(
    "redirect",
    ["https://evil.example/auth", "soloclef://other", "", "soloclef://auth/../x"],
)
def test_any_other_return_address_is_refused(redirect):
    with pytest.raises(AppSignInRefused) as refused:
        parse_app_request(good_params(app_redirect=redirect), ALLOWED)
    assert refused.value.code == BAD_REDIRECT


@pytest.mark.parametrize(
    "over",
    [
        {"challenge": "short"},
        {"challenge": RFC_CHALLENGE + "x"},
        {"challenge": "not base64url!" * 4},
        {"challenge_method": "plain"},
        {"challenge": ""},
    ],
)
def test_a_challenge_that_is_not_s256_is_refused(over):
    with pytest.raises(AppSignInRefused) as refused:
        parse_app_request(good_params(**over), ALLOWED)
    assert refused.value.code == BAD_CHALLENGE


def test_the_device_label_is_tidied_and_capped():
    long = "  Pixel   9 \n Pro " + "x" * 200
    request = parse_app_request(good_params(device=long), ALLOWED)
    assert request.device.startswith("Pixel 9 Pro x")
    assert len(request.device) == app_sign_in.DEVICE_LABEL_MAX
    assert parse_app_request(good_params(device=""), ALLOWED).device == ""


def test_the_allow_list_comes_from_the_environment_and_defaults_to_the_apps_scheme():
    assert (
        parse_app_redirects(None)
        == DEFAULT_APP_REDIRECTS
        == frozenset({"soloclef://auth"})
    )
    assert parse_app_redirects("soloclef://auth, soloclef-dev://auth") == frozenset(
        {"soloclef://auth", "soloclef-dev://auth"}
    )
    settings = load_oidc_settings(
        {"OIDC_APP_REDIRECTS": "soloclef://auth soloclef-dev://auth"}
    )
    assert settings.app_redirects == frozenset(
        {"soloclef://auth", "soloclef-dev://auth"}
    )
    assert "soloclef://auth" in load_oidc_settings({}).app_redirects


# -- PKCE ---------------------------------------------------------------------


def test_the_challenge_is_rfc_7636s_s256_of_the_verifier():
    assert challenge_of(RFC_VERIFIER) == RFC_CHALLENGE
    assert verifier_matches(RFC_VERIFIER, RFC_CHALLENGE)


@pytest.mark.parametrize(
    "verifier",
    [RFC_VERIFIER[:-1] + "l", "", "short", "x" * 129, "has spaces " * 5, None, 42],
)
def test_any_other_verifier_does_not_match(verifier):
    assert not verifier_matches(verifier, RFC_CHALLENGE)


# -- the one-time codes --------------------------------------------------------


def grant(**over):
    base = {
        "user_id": "u-1",
        "challenge": RFC_CHALLENGE,
        "device": "iPhone",
        "issued_at": 1000.0,
    }
    base.update(over)
    return AppGrant(**base)


async def test_a_code_is_spent_once():
    now = [1000.0]
    codes = MemoryAppCodes(clock=lambda: now[0])
    code = await codes.issue(grant())
    assert len(code) >= 43 and "/" not in code and "+" not in code
    assert await codes.redeem(code) == grant()
    assert await codes.redeem(code) is None, "a second redeem finds nothing"


async def test_a_code_dies_after_its_minute():
    now = [1000.0]
    codes = MemoryAppCodes(clock=lambda: now[0])
    code = await codes.issue(grant())
    now[0] = 1000.0 + CODE_LIFETIME_SECONDS - 1
    kept = await codes.issue(grant(user_id="u-2", issued_at=now[0]))
    now[0] = 1000.0 + CODE_LIFETIME_SECONDS
    assert await codes.redeem(code) is None
    assert (await codes.redeem(kept)).user_id == "u-2", "a younger code is still good"


async def test_a_guessed_code_finds_nothing():
    codes = MemoryAppCodes()
    await codes.issue(grant())
    for guess in ["", "x", None, "A" * 43]:
        assert await codes.redeem(guess) is None


def test_the_code_lifetime_is_sixty_seconds():
    assert CODE_LIFETIME_SECONDS == 60


# -- the attempt cookie ---------------------------------------------------------


def test_an_app_attempt_survives_its_cookie_and_a_web_one_carries_no_app():
    app = AppRequest(
        redirect="soloclef://auth",
        challenge=RFC_CHALLENGE,
        device="Pixel 9",
        choose_account=True,
    )
    attempt = LoginAttempt.start(app=app)
    back = reopen(attempt.seal(SECRET), attempt.state, SECRET)
    assert back == attempt
    assert back.app == app
    web = LoginAttempt.start("/dashboard/1")
    assert reopen(web.seal(SECRET), web.state, SECRET).app is None


# -- the provider's URL ---------------------------------------------------------


def settings():
    return OidcSettings(
        enabled=True,
        discovery_url=DISCOVERY_URL,
        client_id=CLIENT_ID,
        redirect_uri=REDIRECT_URI,
    )


def test_use_a_different_address_adds_prompt_select_account_and_nothing_else_does():
    provider = parse_metadata(FakeOidcProvider()._discovery(), DISCOVERY_URL)
    chooser = LoginAttempt.start(
        app=AppRequest("soloclef://auth", RFC_CHALLENGE, "Pixel", choose_account=True)
    )
    plain = LoginAttempt.start(
        app=AppRequest("soloclef://auth", RFC_CHALLENGE, "Pixel")
    )
    web = LoginAttempt.start()

    def query(a):
        url = authorization_url(settings(), provider, a)
        return {k: v[0] for k, v in parse_qs(urlsplit(url).query).items()}

    assert query(chooser)["prompt"] == "select_account"
    assert "prompt" not in query(plain)
    assert "prompt" not in query(web)
    assert query(chooser)["code_challenge_method"] == "S256"
    assert "login" not in query(chooser).get("prompt", "")


def test_the_providers_end_session_page_is_kept_when_it_publishes_one():
    document = FakeOidcProvider()._discovery()
    assert parse_metadata(document, DISCOVERY_URL).end_session_endpoint == str(
        document.get("end_session_endpoint") or ""
    )
    with_end = {
        **document,
        "end_session_endpoint": "https://sso.test/application/o/notes/end-session/",
    }
    assert (
        parse_metadata(with_end, DISCOVERY_URL).end_session_endpoint
        == "https://sso.test/application/o/notes/end-session/"
    )


# -- where the sheet is sent back -----------------------------------------------


def test_the_app_gets_a_code_or_an_error_on_its_own_address():
    assert app_return("soloclef://auth", code="abc") == "soloclef://auth?code=abc"
    assert (
        app_return("soloclef://auth", error="OIDC_NO_ACCOUNT")
        == "soloclef://auth?error=OIDC_NO_ACCOUNT"
    )
    assert app_return("soloclef://auth") == "soloclef://auth?error=OIDC_PROVIDER_ERROR"
    assert app_return("soloclef://auth", code="a b&c") == "soloclef://auth?code=a+b%26c"
