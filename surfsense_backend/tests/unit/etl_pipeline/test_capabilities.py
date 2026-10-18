"""The parser-choice table the pipeline, the billing and the upload UI all read.

These pin its promises: Local is always pymupdf4llm and free; Cloud is
whatever ETL_CLOUD_SERVICE names, billed at the premium rate, and says
plainly when it cannot run; each choice reports the extensions it opens.
"""

import pytest

from app.etl_pipeline.capabilities import (
    UNAVAILABLE_MISSING_API_KEY,
    UNAVAILABLE_UNKNOWN_SERVICE,
    page_multiplier_for,
    parser_capability,
)
from app.etl_pipeline.etl_document import DocumentParser
from app.utils.file_extensions import (
    LLAMAPARSE_DOCUMENT_EXTENSIONS,
    PYMUPDF4LLM_DOCUMENT_EXTENSIONS,
    UNSTRUCTURED_DOCUMENT_EXTENSIONS,
)

pytestmark = pytest.mark.unit


@pytest.fixture
def cloud(monkeypatch):
    def configure(service="LLAMACLOUD", *, api_key="key", azure=False):
        monkeypatch.setattr("app.config.config.ETL_CLOUD_SERVICE", service)
        monkeypatch.setattr("app.config.config.LLAMA_CLOUD_API_KEY", api_key)
        monkeypatch.setattr(
            "app.config.config.AZURE_DI_ENDPOINT",
            "https://di.example" if azure else None,
        )
        monkeypatch.setattr("app.config.config.AZURE_DI_KEY", "k" if azure else None)

    return configure


@pytest.mark.parametrize("cloud_service", ["LLAMACLOUD", "DATALAB", "UNSTRUCTURED"])
def test_local_is_always_pymupdf4llm(cloud, cloud_service):
    cloud(cloud_service)
    local = parser_capability(DocumentParser.LOCAL)
    assert local.service == "PYMUPDF4LLM"
    assert local.available is True
    assert local.document_extensions == PYMUPDF4LLM_DOCUMENT_EXTENSIONS


def test_cloud_is_llamacloud_with_its_extensions(cloud):
    cloud("LLAMACLOUD")
    capability = parser_capability(DocumentParser.CLOUD)
    assert capability.service == "LLAMACLOUD"
    assert capability.available is True
    assert capability.document_extensions == LLAMAPARSE_DOCUMENT_EXTENSIONS


def test_cloud_extensions_grow_with_the_azure_accelerator(cloud):
    cloud("LLAMACLOUD", azure=True)
    assert ".heif" in parser_capability(DocumentParser.CLOUD).document_extensions


def test_cloud_without_a_llamacloud_key_is_unavailable_with_a_reason(cloud):
    cloud("LLAMACLOUD", api_key=None)
    capability = parser_capability(DocumentParser.CLOUD)
    assert capability.available is False
    assert capability.unavailable_reason == UNAVAILABLE_MISSING_API_KEY


def test_cloud_can_be_the_bundled_unstructured_library(cloud):
    pytest.importorskip("langchain_unstructured")
    cloud("unstructured", api_key=None)
    capability = parser_capability(DocumentParser.CLOUD)
    assert capability.service == "UNSTRUCTURED"
    assert capability.available is True
    assert capability.document_extensions == UNSTRUCTURED_DOCUMENT_EXTENSIONS


@pytest.mark.parametrize("service", ["", "PYMUPDF4LLM", "DOCLING", "AZURE"])
def test_an_unrecognised_cloud_service_fails_closed(cloud, service):
    cloud(service)
    capability = parser_capability(DocumentParser.CLOUD)
    assert capability.available is False
    assert capability.unavailable_reason == UNAVAILABLE_UNKNOWN_SERVICE
    assert capability.document_extensions == frozenset()


def test_local_is_free_and_cloud_is_billed_at_the_premium_rate():
    assert page_multiplier_for(DocumentParser.LOCAL) == 0
    assert page_multiplier_for(DocumentParser.CLOUD) == 10
    assert parser_capability(DocumentParser.CLOUD).page_multiplier == 10
