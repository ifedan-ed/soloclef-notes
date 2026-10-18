"""Every document-parser setting the backend reads is documented where
self-hosters look: the "Reading documents" section of the self-hosting guide
(surfsense_web/content/docs/self-hosting.mdx) and docker/.env.example. A new
parser setting in app.config fails here until both name it."""

from __future__ import annotations

import re
from pathlib import Path

import pytest

pytestmark = pytest.mark.unit

ROOT = Path(__file__).resolve().parents[4]
GUIDE = ROOT / "surfsense_web" / "content" / "docs" / "self-hosting.mdx"
ENV_EXAMPLE = ROOT / "docker" / ".env.example"
PREFIXES = ("ETL_", "DATALAB_", "LLAMA_CLOUD_", "AZURE_DI_", "UNSTRUCTURED_")
#: Read from the environment by the llama-cloud SDK, not by app.config;
#: ETL_SERVICE is deprecated and no longer kept on the config.
OUTSIDE_THE_CONFIG = ("LLAMA_CLOUD_BASE_URL", "ETL_SERVICE")


def _parser_settings() -> list[str]:
    from app.config import Config

    names = [name for name in vars(Config) if name.startswith(PREFIXES)]
    return sorted({*names, *OUTSIDE_THE_CONFIG})


def _guide_section() -> str:
    if not GUIDE.exists():
        pytest.skip("surfsense_web is not in this checkout")
    text = GUIDE.read_text()
    match = re.search(r"^## Reading documents\n(.*?)(?=^## |\Z)", text, re.S | re.M)
    assert match, "self-hosting.mdx has no 'Reading documents' section"
    return match.group(1)


def test_the_settings_found_include_the_two_parser_lines():
    assert {"ETL_CLOUD_SERVICE", "ETL_DEFAULT_PARSER"} <= set(_parser_settings())


@pytest.mark.parametrize("name", _parser_settings())
def test_the_self_hosting_guide_names_each_parser_setting(name):
    assert f"`{name}`" in _guide_section()


@pytest.mark.parametrize("name", _parser_settings())
def test_the_env_example_names_each_parser_setting(name):
    assert re.search(rf"\b{name}\b", ENV_EXAMPLE.read_text()), name


def _env_parser_block() -> str:
    """docker/.env.example's parser lines as one line of words (comments unwrapped)."""
    text = ENV_EXAMPLE.read_text()
    start = text.index("# Document parsing.")
    block = text[start : text.index("# Embedding model", start)]
    return " ".join(line.lstrip("#").strip() for line in block.splitlines())


def test_every_old_etl_service_value_is_named_where_it_is_explained():
    # ETL_SERVICE still accepts these (parser_settings); the guide's row and
    # the env example listed some and left AZURE_DI out.
    from app.config.parser_settings import CLOUD_SERVICES, LEGACY_NON_CLOUD_SERVICES

    guide_row = next(
        line
        for line in _guide_section().splitlines()
        if line.startswith("| `ETL_SERVICE`")
    )
    env_block = _env_parser_block()
    for value in (*CLOUD_SERVICES, *LEGACY_NON_CLOUD_SERVICES):
        assert f"`{value}`" in guide_row, value
        assert re.search(rf"\b{value}\b", env_block), value


def test_what_a_cloud_page_costs_is_the_billing_price_not_the_multiplier():
    # "Cloud is billed at 10x" was wrong: a page costs the "Cloud parsing"
    # price (2 credits by default); 10 is only the cost note's multiplier.
    from app.billing.defaults import ACTION_PRICES

    cloud, local = ACTION_PRICES["cloud_page"][0], ACTION_PRICES["local_page"][0]
    block = _env_parser_block()
    assert "billed at 10x" not in block
    assert f"{local:g} and {cloud:g} credits a page by default" in block
