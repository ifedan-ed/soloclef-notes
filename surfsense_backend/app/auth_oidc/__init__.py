"""Sign-in through an OpenID Connect provider (Authentik at sso.example.com).

Password sign-in, reset and the ``/auth/desktop`` routes the mobile app uses
are untouched: this adds a second door beside them, mounted only when
``OIDC_ENABLED=TRUE``. The provider proves who the person is; Notes then
issues its own session exactly as a password sign-in does, so nothing here
depends on the provider's tokens after the callback. See the "Sign-in with
Authentik (OIDC)" section of ``custom/README.md``.
"""
