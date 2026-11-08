from __future__ import annotations

import pytest

from app.db import SearchSourceConnectorType
from app.utils.validators import validate_connector_config

pytestmark = pytest.mark.unit


def test_generic_write_rejects_plaintext_imap_password():
    with pytest.raises(ValueError):
        validate_connector_config(
            SearchSourceConnectorType.EMAIL_IMAP_CONNECTOR,
            {"accounts": [{"host": "evil.example.com", "app_password": "hunter2"}]},
        )


def test_generic_write_allows_already_encrypted_accounts():
    cfg = validate_connector_config(
        SearchSourceConnectorType.EMAIL_IMAP_CONNECTOR,
        {"accounts": [{"_encrypted": True, "app_password": "cipher-text"}]},
    )
    assert cfg["accounts"][0]["app_password"] == "cipher-text"


def test_generic_write_without_accounts_is_untouched():
    cfg = validate_connector_config(
        SearchSourceConnectorType.EMAIL_IMAP_CONNECTOR, {"some_other_key": 1}
    )
    assert cfg == {"some_other_key": 1}


def test_other_connector_types_are_unaffected():
    cfg = validate_connector_config(
        SearchSourceConnectorType.GOOGLE_DRIVE_CONNECTOR,
        {"accounts": [{"app_password": "plaintext-is-fine-for-other-types"}]},
    )
    assert cfg["accounts"][0]["app_password"] == "plaintext-is-fine-for-other-types"
