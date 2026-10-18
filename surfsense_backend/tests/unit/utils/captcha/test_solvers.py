"""Offline checks for the in-house captcha-solver seam.

The network solve can't be unit-tested, but the boundary logic can: a wrong
proxy reformat (2captcha's ``ERROR_PROXY_FORMAT``) or dispatching to a provider
we haven't wired silently burns paid solves / leaks the key to the wrong vendor.
"""

import pytest

from app.utils.captcha import solvers
from app.utils.captcha.config import CaptchaConfig

pytestmark = pytest.mark.unit


def _cfg(**overrides) -> CaptchaConfig:
    base = {
        "enabled": True,
        "solving_site": "2captcha",
        "api_key": "key-123",
        "max_attempts_per_url": 1,
        "timeout_s": 30,
        "captcha_type_default": "v2",
        "v3_min_score": 0.7,
        "v3_action": "verify",
    }
    base.update(overrides)
    return CaptchaConfig(**base)


# --- proxy reformat (2captcha wants login:pass@host:port, NO scheme) --------


def test_proxy_login_form_strips_scheme():
    got = solvers.proxy_login_form("http://user:pass@gw.dataimpulse.com:15673")
    assert got == "user:pass@gw.dataimpulse.com:15673"


def test_proxy_login_form_without_credentials():
    assert (
        solvers.proxy_login_form("http://gw.dataimpulse.com:823")
        == "gw.dataimpulse.com:823"
    )


def test_proxy_login_form_none_on_missing_or_bad():
    assert solvers.proxy_login_form(None) is None
    assert solvers.proxy_login_form("not a url") is None


# --- capsolver proxy reformat (colon-delimited scheme:host:port:user:pass) --


def test_capsolver_proxy_colon_delimited_with_creds():
    got = solvers.capsolver_proxy("http://user:pass@gw.dataimpulse.com:823")
    assert got == "http:gw.dataimpulse.com:823:user:pass"


def test_capsolver_proxy_without_credentials():
    assert (
        solvers.capsolver_proxy("http://gw.dataimpulse.com:823")
        == "http:gw.dataimpulse.com:823"
    )


def test_capsolver_proxy_none_on_missing_or_bad():
    assert solvers.capsolver_proxy(None) is None
    assert solvers.capsolver_proxy("not a url") is None


# --- dispatch --------------------------------------------------------------


def test_unsupported_provider_raises_solvererror():
    # An unconfigured provider must fail loudly (latch) rather than POST the key
    # to a wired vendor's endpoint under the wrong account.
    with pytest.raises(solvers.SolverUnsupportedError):
        solvers.solve(
            _cfg(solving_site="anticaptcha"),
            challenge_type="v2",
            sitekey="SK",
            page_url="https://t.test",
        )
    assert issubclass(solvers.SolverUnsupportedError, solvers.SolverError)


def test_wired_providers_are_registered():
    supported = solvers.supported_providers()
    assert "2captcha" in supported
    assert "capsolver" in supported


# --- vendor reachability (a home proxy on a tailnet is ours alone) ----------


@pytest.mark.parametrize(
    "proxy",
    [
        "http://127.0.0.1:8888",  # tailnet (CGNAT) address
        "http://192.168.1.10:3128",
        "http://127.0.0.1:8888",
        "http://homebox.tail1234.ts.net:8888",
        "http://tinyproxy:8888",  # single-label compose service name
        None,
    ],
)
def test_vendor_cannot_reach_private_proxies(proxy):
    assert solvers.vendor_can_reach(proxy) is False


@pytest.mark.parametrize(
    "proxy",
    ["http://user:pass@gw.dataimpulse.com:823", "http://8.8.8.8:3128"],
)
def test_vendor_can_reach_public_proxies(proxy):
    assert solvers.vendor_can_reach(proxy) is True


def test_solve_goes_proxyless_when_the_vendor_cannot_reach_the_proxy(monkeypatch):
    seen = {}

    def fake_client(cfg, **kwargs):
        seen.update(kwargs)
        return "token"

    monkeypatch.setitem(solvers._PROVIDERS, "2captcha", fake_client)
    token = solvers.solve(
        _cfg(solving_site="2captcha"),
        challenge_type="v2",
        sitekey="SK",
        page_url="https://t.test",
        proxy_url="http://127.0.0.1:8888",
    )
    assert token == "token"
    assert seen["proxy_url"] is None


def test_solve_keeps_a_public_proxy(monkeypatch):
    seen = {}

    def fake_client(cfg, **kwargs):
        seen.update(kwargs)
        return "token"

    monkeypatch.setitem(solvers._PROVIDERS, "2captcha", fake_client)
    solvers.solve(
        _cfg(solving_site="2captcha"),
        challenge_type="v2",
        sitekey="SK",
        page_url="https://t.test",
        proxy_url="http://u:p@gw.dataimpulse.com:15673",
    )
    assert seen["proxy_url"] == "http://u:p@gw.dataimpulse.com:15673"
