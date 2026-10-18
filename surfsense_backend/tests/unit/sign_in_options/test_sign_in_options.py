"""``GET /api/v1/sign-in-options`` (app/sign_in_options): what the phone app
reads before anyone signs in, to know it is talking to a Soloclef server and
which ways in this one offers."""

from __future__ import annotations

import pytest
from fastapi import FastAPI
from starlette.testclient import TestClient

from app.auth_oidc.settings import load_oidc_settings
from app.sign_in_options import provider_name, routes as options_routes, sign_in_options

pytestmark = pytest.mark.unit

OIDC_ENV = {
    "OIDC_ENABLED": "TRUE",
    "OIDC_DISCOVERY_URL": "https://sso.example.org/.well-known/openid-configuration",
    "OIDC_CLIENT_ID": "notes",
    "OIDC_CLIENT_SECRET": "not-a-real-secret",
}


def oidc(env: dict[str, str] | None = None):
    return load_oidc_settings(
        env if env is not None else OIDC_ENV,
        backend_url="https://notes.example.org",
        frontend_url="https://notes.example.org",
    )


def options(**over):
    args = {
        "auth_type": "LOCAL",
        "registration_enabled": True,
        "oidc": oidc(),
        "env": {**OIDC_ENV, "OIDC_BUTTON_LABEL": "Continue with Example SSO"},
    }
    args.update(over)
    return sign_in_options(**args)


def test_a_server_like_notes_example_com_offers_password_reset_request_access_and_its_sso():
    assert options() == {
        "service": "soloclef",
        "password": True,
        "forgot_password": True,
        "sign_up": False,
        "sso": {
            "label": "Continue with Example SSO",
            "name": "Example SSO",
            "app": True,
        },
    }


def test_no_oidc_means_no_sso_button():
    assert options(oidc=oidc({}), env={})["sso"] is None
    # Enabled but missing a setting: app.py leaves the routes out, so no button either.
    incomplete = {k: v for k, v in OIDC_ENV.items() if k != "OIDC_CLIENT_SECRET"}
    assert options(oidc=oidc(incomplete), env=incomplete)["sso"] is None


def test_the_label_defaults_to_the_websites_words():
    sso = options(env=OIDC_ENV)["sso"]
    assert sso == {
        "label": "Continue with single sign-on",
        "name": "single sign-on",
        "app": True,
    }


def test_the_app_is_told_when_its_return_address_is_not_allowed():
    env = {**OIDC_ENV, "OIDC_APP_REDIRECTS": "otherapp://auth"}
    assert options(oidc=oidc(env), env=env)["sso"]["app"] is False


def test_google_only_servers_take_no_password():
    got = options(auth_type="GOOGLE")
    assert (got["password"], got["forgot_password"], got["sign_up"]) == (
        False,
        False,
        False,
    )


def test_the_kill_switch_turns_every_way_off():
    got = options(registration_enabled=False)
    assert (got["password"], got["forgot_password"], got["sso"]) == (False, False, None)
    assert got["service"] == "soloclef"
    # Sign-up too, whatever the website was built with: nothing gets in.
    opened = {**OIDC_ENV, "NEXT_PUBLIC_SIGNUPS_OPEN": "true"}
    assert options(registration_enabled=False, env=opened)["sign_up"] is False


@pytest.mark.parametrize("switch", [None, "", "false", "FALSE", "1"])
def test_sign_ups_closed_at_the_proxy_keep_every_sign_in(switch):
    # How a server closes sign-ups and keeps sign-in (self-hosting.mdx, "Closing
    # sign-ups"): REGISTRATION_ENABLED stays on, the proxy refuses POST
    # /auth/register, and NEXT_PUBLIC_SIGNUPS_OPEN is not "true", so the app
    # offers Request access beside every way in.
    env = {**OIDC_ENV, "OIDC_BUTTON_LABEL": "Continue with Example SSO"}
    if switch is not None:
        env["NEXT_PUBLIC_SIGNUPS_OPEN"] = switch
    got = options(registration_enabled=True, env=env)
    assert (got["password"], got["forgot_password"], got["sign_up"]) == (
        True,
        True,
        False,
    )
    assert got["sso"] == {
        "label": "Continue with Example SSO",
        "name": "Example SSO",
        "app": True,
    }


def test_sign_ups_follow_the_websites_own_switch():
    assert (
        options(env={**OIDC_ENV, "NEXT_PUBLIC_SIGNUPS_OPEN": "true"})["sign_up"] is True
    )
    assert (
        options(env={**OIDC_ENV, "NEXT_PUBLIC_SIGNUPS_OPEN": "TRUE"})["sign_up"]
        is False
    )


@pytest.mark.parametrize(
    ("label", "name"),
    [
        ("Continue with Example SSO", "Example SSO"),
        ("  Sign in with   Okta ", "Okta"),
        ("Log in with Keycloak", "Keycloak"),
        ("Continue with single sign-on", "single sign-on"),
        ("Staff login", "single sign-on"),
        ("Continue with ", "single sign-on"),
    ],
)
def test_the_providers_name_comes_from_the_button(label, name):
    assert provider_name(label) == name


def test_the_route_answers_without_a_sign_in_and_may_be_cached(monkeypatch):
    monkeypatch.setattr(options_routes, "_options", lambda: options())
    app = FastAPI()
    app.include_router(options_routes.router, prefix="/api/v1")
    response = TestClient(app).get("/api/v1/sign-in-options")
    assert response.status_code == 200
    assert response.headers["cache-control"] == "public, max-age=60"
    assert response.json()["service"] == "soloclef"
    assert response.json()["sso"]["name"] == "Example SSO"


def test_the_route_reads_the_servers_own_environment(monkeypatch):
    # What the backend sees from docker/.env (env_file) and docker-compose.yml.
    from app.config import config

    for name, value in {
        **OIDC_ENV,
        "OIDC_BUTTON_LABEL": "Continue with Example SSO",
    }.items():
        monkeypatch.setenv(name, value)
    for name in ("NEXT_PUBLIC_SIGNUPS_OPEN", "OIDC_APP_REDIRECTS"):
        monkeypatch.delenv(name, raising=False)
    monkeypatch.setattr(config, "AUTH_TYPE", "LOCAL")
    monkeypatch.setattr(config, "REGISTRATION_ENABLED", True)
    monkeypatch.setattr(config, "BACKEND_URL", "https://notes.example.org")
    monkeypatch.setattr(config, "NEXT_FRONTEND_URL", "https://notes.example.org")
    options_routes._options.cache_clear()
    try:
        assert options_routes._options() == options()
        options_routes._options.cache_clear()
        monkeypatch.setenv("NEXT_PUBLIC_SIGNUPS_OPEN", "true")
        assert options_routes._options()["sign_up"] is True
    finally:
        options_routes._options.cache_clear()


def test_the_route_is_mounted_under_api_v1():
    from app.routes import router

    paths = {getattr(route, "path", None) for route in router.routes}
    assert "/sign-in-options" in paths
