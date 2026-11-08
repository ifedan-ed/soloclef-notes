"""What a hand-off can be for, and the fixed app address each one returns to."""

from __future__ import annotations

import pytest

from app.app_handoff.purposes import (
    APP_RETURNS,
    CONNECTOR_PURPOSES,
    DRIVE_CONNECT,
    DRIVE_PICKER,
    MICROSOFT_MAIL,
    PLANNED_PURPOSES,
    app_return,
    is_supported,
)

pytestmark = pytest.mark.unit


def test_the_picker_and_both_connector_sign_ins_are_built_nothing_else():
    for purpose in (DRIVE_PICKER, DRIVE_CONNECT, MICROSOFT_MAIL):
        assert is_supported(purpose)
    assert {DRIVE_CONNECT, MICROSOFT_MAIL} == CONNECTOR_PURPOSES
    assert frozenset() == PLANNED_PURPOSES
    assert not is_supported("anything")


def test_the_drive_picker_returns_to_the_apps_drive_address():
    assert APP_RETURNS[DRIVE_PICKER] == "soloclef://drive/done"
    assert app_return(DRIVE_PICKER) == "soloclef://drive/done"
    assert (
        app_return(DRIVE_PICKER, count=3, unsupported=1)
        == "soloclef://drive/done?count=3&unsupported=1"
    )
    assert (
        app_return(DRIVE_PICKER, error="google_drive_reconnect")
        == "soloclef://drive/done?error=google_drive_reconnect"
    )
