"""The two parser settings and the deprecated ETL_SERVICE, read at startup.

Pinned: ETL_CLOUD_SERVICE names Cloud (LLAMACLOUD when unset) and
ETL_DEFAULT_PARSER the default choice (LOCAL when unset); an unknown value
of either stops startup with a message naming the allowed ones; the old
ETL_SERVICE counts only while ETL_CLOUD_SERVICE is unset, and is warned
about whenever it is set.
"""

from __future__ import annotations

import pytest

from app.config.parser_settings import (
    CLOUD_SERVICES,
    ParserSettingError,
    resolve_parser_settings,
)

pytestmark = pytest.mark.unit


def _resolve(**environ: str):
    return resolve_parser_settings(environ)


def test_unset_means_cloud_is_llamacloud_and_the_default_is_local():
    settings = _resolve()
    assert settings.cloud_service == "LLAMACLOUD"
    assert settings.default_parser == "LOCAL"
    assert settings.legacy_warning is None


@pytest.mark.parametrize("service", CLOUD_SERVICES)
def test_each_cloud_service_can_be_named_in_any_case(service):
    settings = _resolve(ETL_CLOUD_SERVICE=f" {service.lower()} ")
    assert settings.cloud_service == service
    assert settings.legacy_warning is None


@pytest.mark.parametrize(
    ("value", "expected"), [("cloud", "CLOUD"), (" LOCAL ", "LOCAL")]
)
def test_the_default_parser_is_local_or_cloud(value, expected):
    assert _resolve(ETL_DEFAULT_PARSER=value).default_parser == expected


def test_empty_values_count_as_unset():
    """Compose passes an unset line as an empty string."""
    settings = _resolve(ETL_CLOUD_SERVICE="", ETL_DEFAULT_PARSER="", ETL_SERVICE=" ")
    assert (settings.cloud_service, settings.default_parser) == ("LLAMACLOUD", "LOCAL")
    assert settings.legacy_warning is None


@pytest.mark.parametrize("value", ["LLAMACLOD", "PYMUPDF4LLM", "AZURE_DI", "DOCLING"])
def test_an_unknown_cloud_service_stops_startup_naming_the_allowed_ones(value):
    with pytest.raises(ParserSettingError) as info:
        _resolve(ETL_CLOUD_SERVICE=value)
    message = str(info.value)
    assert f"ETL_CLOUD_SERVICE={value}" in message
    assert "LLAMACLOUD, DATALAB or UNSTRUCTURED" in message


@pytest.mark.parametrize("value", ["PREMIUM", "basic", "auto"])
def test_an_unknown_default_parser_stops_startup_naming_the_allowed_ones(value):
    with pytest.raises(ParserSettingError) as info:
        _resolve(ETL_DEFAULT_PARSER=value)
    message = str(info.value)
    assert f"ETL_DEFAULT_PARSER={value.upper()}" in message
    assert "LOCAL or CLOUD" in message


# --- the deprecated ETL_SERVICE ------------------------------------------


def test_etl_service_is_ignored_with_a_warning_when_etl_cloud_service_is_set():
    """This server's case: ETL_SERVICE=LLAMACLOUD beside ETL_CLOUD_SERVICE=DATALAB."""
    settings = _resolve(
        ETL_SERVICE="LLAMACLOUD",
        ETL_CLOUD_SERVICE="DATALAB",
        ETL_DEFAULT_PARSER="CLOUD",
    )
    assert settings.cloud_service == "DATALAB"
    assert settings.default_parser == "CLOUD"
    assert settings.legacy_warning.startswith(
        "ETL_SERVICE is ignored because ETL_CLOUD_SERVICE is set"
    )


def test_an_ignored_etl_service_is_not_checked():
    settings = _resolve(ETL_SERVICE="anything", ETL_CLOUD_SERVICE="DATALAB")
    assert settings.cloud_service == "DATALAB"
    assert "ignored" in settings.legacy_warning


@pytest.mark.parametrize("legacy", CLOUD_SERVICES)
def test_alone_a_cloud_etl_service_names_cloud_with_a_deprecation_warning(legacy):
    settings = _resolve(ETL_SERVICE=legacy.lower())
    assert settings.cloud_service == legacy
    assert settings.default_parser == "LOCAL"
    assert settings.legacy_warning.startswith(
        "ETL_SERVICE is deprecated; set ETL_CLOUD_SERVICE"
    )


@pytest.mark.parametrize("legacy", ["PYMUPDF4LLM", "DOCLING", "AZURE_DI"])
def test_alone_a_local_etl_service_leaves_cloud_at_its_default(legacy):
    settings = _resolve(ETL_SERVICE=legacy)
    assert settings.cloud_service == "LLAMACLOUD"
    assert settings.legacy_warning.startswith(
        "ETL_SERVICE is deprecated; set ETL_CLOUD_SERVICE"
    )
    assert f"ETL_SERVICE={legacy} names no cloud parser" in settings.legacy_warning


def test_alone_an_unknown_etl_service_stops_startup():
    with pytest.raises(ParserSettingError) as info:
        _resolve(ETL_SERVICE="LLAMA")
    message = str(info.value)
    assert "ETL_SERVICE=LLAMA is not a parser" in message
    assert "ETL_CLOUD_SERVICE to LLAMACLOUD, DATALAB or UNSTRUCTURED" in message


def test_the_running_config_holds_resolved_values():
    from app.config import config

    assert config.ETL_CLOUD_SERVICE in CLOUD_SERVICES
    assert config.ETL_DEFAULT_PARSER in ("LOCAL", "CLOUD")
    # The pre-split setting is not kept, so nothing can read it by mistake.
    assert not hasattr(config, "ETL_SERVICE")
