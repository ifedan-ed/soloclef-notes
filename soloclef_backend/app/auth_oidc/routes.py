"""``/auth/oidc/authorize-redirect`` and ``/auth/oidc/callback``.

Every refusal ends on ``/login?error=<code>``, where the page explains it,
instead of a bare JSON error in the browser tab. A sign-in the phone app
started (``app_sign_in.py``, ``app_routes.py``) shares this callback, since
Authentik only ever returns to the one registered address: it ends on the
app's own address instead, with a one-time code or the error's code.
"""

from __future__ import annotations

import logging
import time
from collections.abc import Callable, Sequence
from urllib.parse import urlencode

from fastapi import APIRouter, Depends, HTTPException, Request
from fastapi.params import Depends as DependsParam
from fastapi.responses import RedirectResponse
from sqlalchemy.ext.asyncio import AsyncSession

from app.auth_oidc import account_creation_rule, login_attempt
from app.auth_oidc.account_link import account_for
from app.auth_oidc.app_sign_in import AppCodeStore, AppGrant, app_return
from app.auth_oidc.authorization_request import authorization_url
from app.auth_oidc.discovery import ProviderDirectory
from app.auth_oidc.errors import (
    PROVIDER_ERRORS_SHOWN_AS_IS,
    PROVIDER_REFUSED,
    RATE_LIMITED,
    OidcLoginError,
)
from app.auth_oidc.id_token import verify_id_token
from app.auth_oidc.settings import OidcSettings
from app.auth_oidc.token_exchange import exchange_code
from app.auth_oidc.web_session import signed_in_redirect
from app.db import get_async_session
from app.users import get_user_manager

logger = logging.getLogger(__name__)


def build_oidc_router(
    settings: OidcSettings,
    *,
    secret: str,
    rate_limit: Callable[[Request], None],
    guards: Sequence[DependsParam] = (),
    directory: ProviderDirectory | None = None,
    app_codes: AppCodeStore | None = None,
) -> APIRouter:
    """The two routes, bound to one provider.

    ``rate_limit`` raises ``HTTPException(429)`` when a client IP has used its
    ``settings.login_attempts_per_minute``; ``guards`` are the dependencies
    every other sign-in route has (the REGISTRATION_ENABLED kill switch).
    ``app_codes`` holds the phone app's one-time codes; without it an app's
    sign-in is refused at the callback.
    """
    directory = directory or ProviderDirectory(settings.discovery_url)
    logger.info(
        "OIDC sign-in on; a first sign-in with no account creates one for %s",
        account_creation_rule.describe(
            allow_signup=settings.allow_signup,
            autocreate_domains=settings.autocreate_domains,
        ),
    )
    router = APIRouter(prefix="/auth/oidc", tags=["auth"], dependencies=list(guards))

    def to_login(code: str) -> RedirectResponse:
        query = urlencode({"error": code})
        response = RedirectResponse(f"{settings.frontend_url}/login?{query}", 303)
        response.delete_cookie(
            login_attempt.COOKIE_NAME, path=login_attempt.COOKIE_PATH
        )
        return response

    def within_rate_limit(request: Request) -> bool:
        try:
            rate_limit(request)
        except HTTPException as exc:
            if exc.status_code == 429:
                return False
            raise
        return True

    @router.get("/authorize-redirect", name="oidc_authorize_redirect")
    async def authorize_redirect(request: Request) -> RedirectResponse:
        """Send the browser to the provider's sign-in, remembering this attempt.

        ``?next=/some/page`` is where to land afterwards (a path on this site;
        anything else lands on the dashboard).
        """
        if not within_rate_limit(request):
            return to_login(RATE_LIMITED)
        try:
            provider = await directory.metadata()
        except OidcLoginError as exc:
            logger.warning("OIDC sign-in could not start: %s", exc)
            return to_login(exc.code)

        attempt = login_attempt.LoginAttempt.start(request.query_params.get("next"))
        response = RedirectResponse(
            authorization_url(settings, provider, attempt), status_code=302
        )
        response.set_cookie(
            login_attempt.COOKIE_NAME,
            attempt.seal(secret),
            max_age=login_attempt.LIFETIME_SECONDS,
            path=login_attempt.COOKIE_PATH,
            httponly=True,
            secure=settings.redirect_uri.startswith("https://"),
            # Lax: sent on the provider's top-level redirect back to Notes.
            samesite="lax",
        )
        return response

    def to_app(app, code: str) -> RedirectResponse:
        """A refused app sign-in: back to the app with the reason (never to /login)."""
        response = RedirectResponse(app_return(app.redirect, error=code), 303)
        response.delete_cookie(
            login_attempt.COOKIE_NAME, path=login_attempt.COOKIE_PATH
        )
        return response

    @router.get("/callback", name="oidc_callback")
    async def callback(
        request: Request,
        session: AsyncSession = Depends(get_async_session),
        user_manager=Depends(get_user_manager),
    ) -> RedirectResponse:
        """Finish the sign-in the provider sent back, or say why it cannot."""
        if not within_rate_limit(request):
            return to_login(RATE_LIMITED)
        params = request.query_params
        attempt = None
        try:
            # The attempt first: a refusal after this point goes back to
            # whoever started the sign-in (the app, or the login page).
            try:
                attempt = login_attempt.reopen(
                    request.cookies.get(login_attempt.COOKIE_NAME),
                    params.get("state"),
                    secret,
                )
            except OidcLoginError:
                # A provider's refusal with no matching cookie (a sign-in
                # older than the cookie, another browser, blocked cookies) is
                # still explained as the provider's on the login page, as it
                # was before app sign-ins: only a found attempt is an app's.
                if not params.get("error"):
                    raise
            provider_error = params.get("error")
            if provider_error:
                code = (
                    provider_error
                    if provider_error in PROVIDER_ERRORS_SHOWN_AS_IS
                    else PROVIDER_REFUSED
                )
                raise OidcLoginError(code, f"provider answered {provider_error}")

            code = params.get("code")
            if not code:
                raise OidcLoginError(PROVIDER_REFUSED, "callback carries no code")

            id_token = await exchange_code(settings, directory, attempt, code)
            identity = await verify_id_token(id_token, settings, directory, attempt)
            account = await account_for(
                identity,
                provider_name=settings.provider_name,
                allow_signup=settings.allow_signup,
                autocreate_domains=settings.autocreate_domains,
                session=session,
                user_manager=user_manager,
            )
        except OidcLoginError as exc:
            logger.warning("OIDC sign-in refused (%s): %s", exc.code, exc)
            if attempt is not None and attempt.app is not None:
                return to_app(attempt.app, exc.code)
            return to_login(exc.code)

        if attempt.app is not None:
            # The phone app: no cookies. A one-time code goes back to the app,
            # which spends it with its PKCE verifier (app_routes.py); the
            # session is made there, so on_after_login runs there too.
            if app_codes is None:
                logger.warning("OIDC app sign-in refused: no code store")
                return to_app(attempt.app, PROVIDER_REFUSED)
            one_time = await app_codes.issue(
                AppGrant(
                    user_id=str(account.user.id),
                    challenge=attempt.app.challenge,
                    device=attempt.app.device,
                    issued_at=time.time(),
                )
            )
            logger.info(
                "OIDC app sign-in for user %s (%s)",
                account.user.id,
                account.match.value,
            )
            response = RedirectResponse(
                app_return(attempt.app.redirect, code=one_time), 303
            )
            response.delete_cookie(
                login_attempt.COOKIE_NAME, path=login_attempt.COOKIE_PATH
            )
            return response

        logger.info(
            "OIDC sign-in for user %s (%s)", account.user.id, account.match.value
        )
        response = await signed_in_redirect(
            account.user, request, settings.frontend_url, attempt.return_to
        )
        response.delete_cookie(
            login_attempt.COOKIE_NAME, path=login_attempt.COOKIE_PATH
        )
        await user_manager.on_after_login(account.user, request, response)
        return response

    return router
