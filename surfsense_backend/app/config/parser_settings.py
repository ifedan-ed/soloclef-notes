"""The two document-parser settings, read once at startup.

Each upload is read by Local (pymupdf4llm, on this server) or Cloud.
``ETL_CLOUD_SERVICE`` names the service behind Cloud and
``ETL_DEFAULT_PARSER`` the choice made when an upload or a connector names
none. A value outside the allowed ones stops the server at startup with a
message naming them, rather than leaving Cloud silently unavailable.

``ETL_SERVICE`` is the single, deployment-wide setting from before the
Local/Cloud split. Other servers may still carry it, so it is still read,
but only when ``ETL_CLOUD_SERVICE`` is unset, and either way it earns one
warning at startup. Documents keep an ``ETL_SERVICE`` key in their metadata;
that records which parser read each one and is not this setting.
"""

from __future__ import annotations

import os
from collections.abc import Mapping
from dataclasses import dataclass
from typing import Final

CLOUD_SERVICES: Final = ("LLAMACLOUD", "DATALAB", "UNSTRUCTURED")
DEFAULT_CLOUD_SERVICE: Final = "LLAMACLOUD"
PARSER_CHOICES: Final = ("LOCAL", "CLOUD")
DEFAULT_PARSER_CHOICE: Final = "LOCAL"

#: Values the old ETL_SERVICE took that name a local parser (Docling was
#: removed; Azure DI is now an accelerator inside LLAMACLOUD). They say
#: nothing about Cloud, which then keeps its default.
LEGACY_NON_CLOUD_SERVICES: Final = ("PYMUPDF4LLM", "DOCLING", "AZURE_DI")


class ParserSettingError(ValueError):
    """A parser setting holds a value this server cannot run."""


@dataclass(frozen=True)
class ParserSettings:
    cloud_service: str
    default_parser: str
    #: The one startup warning about ETL_SERVICE; None when it is unset.
    legacy_warning: str | None


def _clean(value: str | None) -> str:
    return (value or "").strip().upper()


def _allowed(values: tuple[str, ...]) -> str:
    return ", ".join(values[:-1]) + f" or {values[-1]}"


def _cloud_service(cloud: str, legacy: str) -> tuple[str, str | None]:
    if cloud:
        if cloud not in CLOUD_SERVICES:
            raise ParserSettingError(
                f"ETL_CLOUD_SERVICE={cloud} is not a cloud parser. Set it to "
                f"{_allowed(CLOUD_SERVICES)}, or remove it for {DEFAULT_CLOUD_SERVICE}."
            )
        if legacy:
            return cloud, (
                "ETL_SERVICE is ignored because ETL_CLOUD_SERVICE is set "
                f"(Cloud is {cloud}); remove the ETL_SERVICE line."
            )
        return cloud, None

    if not legacy:
        return DEFAULT_CLOUD_SERVICE, None
    if legacy in CLOUD_SERVICES:
        return legacy, (
            "ETL_SERVICE is deprecated; set ETL_CLOUD_SERVICE. Reading "
            f"ETL_SERVICE={legacy} as ETL_CLOUD_SERVICE={legacy}."
        )
    if legacy in LEGACY_NON_CLOUD_SERVICES:
        return DEFAULT_CLOUD_SERVICE, (
            f"ETL_SERVICE is deprecated; set ETL_CLOUD_SERVICE. ETL_SERVICE={legacy} "
            f"names no cloud parser, so Cloud is {DEFAULT_CLOUD_SERVICE}; Local is "
            "always pymupdf4llm and ETL_DEFAULT_PARSER picks the default."
        )
    raise ParserSettingError(
        f"ETL_SERVICE={legacy} is not a parser, and ETL_SERVICE is deprecated. "
        f"Set ETL_CLOUD_SERVICE to {_allowed(CLOUD_SERVICES)} and remove ETL_SERVICE."
    )


def _default_parser(value: str) -> str:
    if not value:
        return DEFAULT_PARSER_CHOICE
    if value not in PARSER_CHOICES:
        raise ParserSettingError(
            f"ETL_DEFAULT_PARSER={value} is not a parser choice. Set it to "
            f"{_allowed(PARSER_CHOICES)}, or remove it for {DEFAULT_PARSER_CHOICE}."
        )
    return value


def resolve_parser_settings(
    environ: Mapping[str, str] | None = None,
) -> ParserSettings:
    """The parser settings ``environ`` asks for; raises ParserSettingError if unusable."""
    environ = os.environ if environ is None else environ
    cloud, warning = _cloud_service(
        _clean(environ.get("ETL_CLOUD_SERVICE")), _clean(environ.get("ETL_SERVICE"))
    )
    return ParserSettings(
        cloud_service=cloud,
        default_parser=_default_parser(_clean(environ.get("ETL_DEFAULT_PARSER"))),
        legacy_warning=warning,
    )
