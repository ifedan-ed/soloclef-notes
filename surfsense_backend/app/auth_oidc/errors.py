"""Why an OIDC sign-in was refused, as the codes the login page explains."""

from __future__ import annotations

# The login page (surfsense_web/lib/auth-errors.ts) maps each code to a
# sentence; keep the two lists in step.
INVALID_STATE = "OIDC_INVALID_STATE"
PROVIDER_UNAVAILABLE = "OIDC_UNAVAILABLE"
PROVIDER_REFUSED = "OIDC_PROVIDER_ERROR"
EXCHANGE_FAILED = "OIDC_EXCHANGE_FAILED"
INVALID_ID_TOKEN = "OIDC_INVALID_ID_TOKEN"
EMAIL_NOT_VERIFIED = "OIDC_EMAIL_NOT_VERIFIED"
#: The Notes account with the identity's email never verified that address
#: (account_link.decide): not linked, so a planted account takes nobody's SSO.
ACCOUNT_NOT_VERIFIED = "OIDC_ACCOUNT_NOT_VERIFIED"
NO_ACCOUNT = "OIDC_NO_ACCOUNT"
USER_INACTIVE = "USER_INACTIVE"
RATE_LIMITED = "RATE_LIMIT_EXCEEDED"

# Errors the provider itself may send back on the callback (RFC 6749 4.1.2.1)
# that the login page already words; anything else becomes PROVIDER_REFUSED.
PROVIDER_ERRORS_SHOWN_AS_IS = frozenset(
    {"access_denied", "temporarily_unavailable", "server_error"}
)


class OidcLoginError(Exception):
    """A sign-in that must end on the login page with ``code`` explained."""

    def __init__(self, code: str, reason: str = "") -> None:
        super().__init__(reason or code)
        self.code = code
