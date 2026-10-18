"""How a (re)connect rewrites the connector row's grant."""

import pytest

from app.config import config
from app.connectors.google_drive.connect.stored_grant import replace_grant
from app.utils.oauth_security import TokenEncryption

pytestmark = pytest.mark.unit

GRANT = {
    "token": "ya29.access",
    "refresh_token": "1//refresh",
    "client_id": "cid",
    "client_secret": "csecret",
    "token_uri": "https://oauth2.googleapis.com/token",
    "scopes": ["https://www.googleapis.com/auth/drive.file"],
}


def test_tokens_and_the_client_secret_are_stored_encrypted():
    stored = replace_grant(None, GRANT, "a@example.com")
    assert stored["_token_encrypted"] is True
    for name in ("token", "refresh_token", "client_secret"):
        assert stored[name] != GRANT[name]
        assert (
            TokenEncryption(config.SECRET_KEY).decrypt_token(stored[name])
            == GRANT[name]
        )
    assert stored["account_email"] == "a@example.com"
    assert stored["auth_expired"] is False


def test_reconnecting_the_same_account_keeps_its_picks():
    previous = {
        "account_email": "a@example.com",
        "selected_files": [{"id": "f1", "name": "x"}],
        "auth_expired": True,
    }
    stored = replace_grant(previous, GRANT, "a@example.com")
    assert stored["selected_files"] == [{"id": "f1", "name": "x"}]
    assert stored["auth_expired"] is False


def test_another_account_starts_without_the_old_accounts_picks():
    previous = {"account_email": "a@example.com", "selected_files": [{"id": "f1"}]}
    stored = replace_grant(previous, GRANT, "b@example.com")
    assert "selected_files" not in stored
