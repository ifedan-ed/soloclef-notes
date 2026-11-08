"""Password reset by email: the message, and sending it without giving anything away.

fastapi-users owns the tokens (``POST /auth/forgot-password`` and
``POST /auth/reset-password``): each is a signed JWT that carries a
fingerprint of the current password hash, so it stops working the moment the
password changes (single use) and after
``PASSWORD_RESET_TOKEN_LIFETIME_SECONDS``. This package only mails the link.
"""
