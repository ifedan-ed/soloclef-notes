"""The phone app's two routes (plan item B1): ``/api/v1/auth/oidc/app/start``
and ``/api/v1/auth/oidc/app/exchange``. The logic is ``app_sign_in.py``; the
callback that ends a started sign-in with a one-time code is in
``routes.py`` (the web's callback, which Authentik is registered to).

    GET  /start?app_redirect=soloclef://auth&challenge=…&challenge_method=S256
               &device=…[&choose_account=1]
         -> 302 to Example SSO (the attempt in the signed cookie, as the web's)
    POST /exchange {code, verifier, device_label}
         -> the app's session: RefreshTokenResponse + session_id + end_session_url

A refused start redirects to the app with ``?error=<code>`` when the return
address is allowed (the sheet closes and the app says why), and answers 400
otherwise (nothing to redirect to). A refused exchange answers 400 with the
code in ``detail`` (``APP_CODE_EXPIRED``, ``APP_WRONG_VERIFIER``).
"""

from __future__ import annotations

import logging
from collections.abc import Callable, Sequence
from datetime import UTC, datetime, timedelta

from fastapi import APIRouter, Depends, HTTPException, Request, status
from fastapi.params import Depends as DependsParam
from fastapi.responses import RedirectResponse
from sqlalchemy import select

from app.auth.session_cookies import access_expires_at
from app.auth_oidc import login_attempt
from app.auth_oidc.app_sign_in import (
    CODE_EXPIRED,
    WRONG_VERIFIER,
    AppCodeStore,
    AppSignInRefused,
    app_return,
    parse_app_request,
    verifier_matches,
)
from app.auth_oidc.authorization_request import authorization_url
from app.auth_oidc.discovery import ProviderDirectory
from app.auth_oidc.errors import RATE_LIMITED, OidcLoginError
from app.auth_oidc.settings import OidcSettings
from app.config import config
from app.db import User, async_session_maker
from app.schemas.auth import AppExchangeRequest, AppSessionResponse
from app.users import get_jwt_strategy, get_user_manager
from app.utils.refresh_tokens import KIND_APP, issue_refresh_token

logger = logging.getLogger(__name__)

PREFIX = "/api/v1/auth/oidc/app"


def build_app_sign_in_router(
    settings: OidcSettings,
    *,
    secret: str,
    rate_limit: Callable[[Request], None],
    codes: AppCodeStore,
    directory: ProviderDirectory,
    guards: Sequence[DependsParam] = (),
) -> APIRouter:
    router = APIRouter(prefix=PREFIX, tags=["auth"], dependencies=list(guards))

    def within_rate_limit(request: Request) -> bool:
        try:
            rate_limit(request)
        except HTTPException as exc:
            if exc.status_code == 429:
                return False
            raise
        return True

    @router.get("/start", name="oidc_app_start")
    async def start(request: Request) -> RedirectResponse:
        """Send the phone's browser sheet to Example SSO, remembering the app's request."""
        try:
            app = parse_app_request(request.query_params, settings.app_redirects)
        except AppSignInRefused as exc:
            logger.warning("app sign-in could not start: %s", exc)
            raise HTTPException(
                status_code=status.HTTP_400_BAD_REQUEST, detail=exc.code
            ) from exc
        if not within_rate_limit(request):
            return RedirectResponse(app_return(app.redirect, error=RATE_LIMITED), 303)
        try:
            provider = await directory.metadata()
        except OidcLoginError as exc:
            logger.warning("app sign-in could not start: %s", exc)
            return RedirectResponse(app_return(app.redirect, error=exc.code), 303)

        attempt = login_attempt.LoginAttempt.start(app=app)
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
            samesite="lax",
        )
        return response

    @router.post(
        "/exchange", response_model=AppSessionResponse, name="oidc_app_exchange"
    )
    async def exchange(
        request: Request,
        body: AppExchangeRequest,
        user_manager=Depends(get_user_manager),
    ) -> AppSessionResponse:
        """The app's session for its one-time code and the verifier behind the challenge."""
        if not within_rate_limit(request):
            raise HTTPException(status_code=429, detail=RATE_LIMITED)
        # Spent on the way out (GETDEL): a code is redeemed once, whoever
        # presents it. A wrong verifier therefore spends it too; the real
        # app simply signs in again (its own code was 32 random bytes).
        grant = await codes.redeem(body.code)
        if grant is None:
            raise HTTPException(
                status_code=status.HTTP_400_BAD_REQUEST, detail=CODE_EXPIRED
            )
        if not verifier_matches(body.verifier, grant.challenge):
            logger.warning(
                "app sign-in exchange with a verifier that is not the challenge's"
            )
            raise HTTPException(
                status_code=status.HTTP_400_BAD_REQUEST, detail=WRONG_VERIFIER
            )

        async with async_session_maker() as session:
            user = (
                (await session.execute(select(User).where(User.id == grant.user_id)))
                .scalars()
                .first()
            )
        if user is None or not user.is_active:
            raise HTTPException(
                status_code=status.HTTP_401_UNAUTHORIZED, detail="USER_INACTIVE"
            )

        device = (
            " ".join((body.device_label or grant.device or "").split())[:100] or None
        )
        access = await get_jwt_strategy().write_token(user)
        issued = await issue_refresh_token(
            user.id,
            kind=KIND_APP,
            device_label=device,
            absolute_expiry=datetime.now(UTC)
            + timedelta(seconds=config.APP_SESSION_LIFETIME_SECONDS),
        )
        await user_manager.on_after_login(user, request, None)
        try:
            end_session = (await directory.metadata()).end_session_endpoint or None
        except OidcLoginError:
            end_session = None
        logger.info(
            "app sign-in for user %s (%s)", user.id, device or "no device label"
        )
        return AppSessionResponse(
            access_token=access,
            refresh_token=issued.token,
            access_expires_at=access_expires_at(access),
            session_id=str(issued.family_id),
            end_session_url=end_session
            if end_session and end_session.startswith("https://")
            else None,
        )

    return router
