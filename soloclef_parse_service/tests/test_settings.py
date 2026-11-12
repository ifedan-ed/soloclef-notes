import pytest

from parse_service.settings import SettingError, load

COMPLETE = {
    "PARSE_SERVICE_LISTEN": "0.0.0.0:8080",
    "PARSE_SERVICE_WORKERS": "2",
    "PARSE_SERVICE_MAX_BYTES": "1048576",
    "PARSE_SERVICE_PARSE_TIMEOUT_SECONDS": "300",
    "PARSE_SERVICE_QUEUE_WAIT_SECONDS": "2.5",
}


def test_every_setting_is_read():
    settings = load(COMPLETE)
    assert (settings.host, settings.port) == ("0.0.0.0", 8080)
    assert settings.workers == 2
    assert settings.max_bytes == 1048576
    assert settings.parse_timeout_seconds == 300
    assert settings.queue_wait_seconds == 2.5


@pytest.mark.parametrize("name", sorted(COMPLETE))
def test_a_missing_setting_is_named_not_defaulted(name):
    environ = {key: value for key, value in COMPLETE.items() if key != name}
    with pytest.raises(SettingError, match=name):
        load(environ)


@pytest.mark.parametrize(
    ("name", "value"),
    [
        ("PARSE_SERVICE_LISTEN", "8080"),
        ("PARSE_SERVICE_WORKERS", "0"),
        ("PARSE_SERVICE_WORKERS", "two"),
        ("PARSE_SERVICE_MAX_BYTES", "-1"),
        ("PARSE_SERVICE_PARSE_TIMEOUT_SECONDS", "soon"),
    ],
)
def test_an_unusable_value_is_refused(name, value):
    with pytest.raises(SettingError, match=name):
        load({**COMPLETE, name: value})
