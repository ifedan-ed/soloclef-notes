"""What an upload of a file no parser can read is told.

The refusal is the only thing a user sees when a format this deployment
cannot read is dropped on the upload dialog, so it names the file, the
parser that refused it, the formats that parser does read, and the other
parser when that one reads it.
"""

from __future__ import annotations

import pytest

from app.etl_pipeline.etl_document import DocumentParser
from app.etl_pipeline.unreadable_file import (
    unreadable_file_message,
    unsupported_file_type_detail,
)

pytestmark = pytest.mark.unit


@pytest.fixture
def local_only(monkeypatch):
    """Local available, Cloud not: the shape of a deployment with no keys."""
    monkeypatch.setattr("app.config.config.ETL_CLOUD_SERVICE", "LLAMACLOUD")
    monkeypatch.setattr("app.config.config.LLAMA_CLOUD_API_KEY", None)
    monkeypatch.setattr("app.config.config.AZURE_DI_ENDPOINT", None, raising=False)
    monkeypatch.setattr("app.config.config.AZURE_DI_KEY", None, raising=False)


@pytest.fixture
def cloud_too(monkeypatch):
    monkeypatch.setattr("app.config.config.ETL_CLOUD_SERVICE", "LLAMACLOUD")
    monkeypatch.setattr("app.config.config.LLAMA_CLOUD_API_KEY", "key")
    monkeypatch.setattr("app.config.config.AZURE_DI_ENDPOINT", None, raising=False)
    monkeypatch.setattr("app.config.config.AZURE_DI_KEY", None, raising=False)


def test_the_machine_readable_fields_are_unchanged(local_only):
    detail = unsupported_file_type_detail(["sheet.xlsx"], DocumentParser.LOCAL)
    assert detail["code"] == "unsupported_file_type"
    assert detail["parser"] == "local"
    assert detail["files"] == ["sheet.xlsx"]


def test_the_message_names_the_file_and_the_formats_that_work(local_only):
    detail = unsupported_file_type_detail(["sheet.xlsx"], DocumentParser.LOCAL)
    message = detail["message"]
    assert "sheet.xlsx" in message
    assert ".pdf" in message and ".docx" in message
    assert ".xlsx" not in detail["accepted_extensions"]
    assert ".pdf" in detail["accepted_extensions"]
    # Sorted, so the list reads the same every time.
    assert detail["accepted_extensions"] == sorted(detail["accepted_extensions"])


def test_the_message_says_the_other_parser_reads_it_when_it_does(cloud_too):
    message = unsupported_file_type_detail(["sheet.xlsx"], DocumentParser.LOCAL)[
        "message"
    ]
    assert "Cloud" in message


def test_the_message_does_not_offer_a_parser_that_cannot_run(local_only):
    message = unsupported_file_type_detail(["sheet.xlsx"], DocumentParser.LOCAL)[
        "message"
    ]
    assert "Cloud" not in message


def test_the_message_does_not_offer_a_parser_that_also_cannot_read_it(cloud_too):
    message = unsupported_file_type_detail(["program.exe"], DocumentParser.LOCAL)[
        "message"
    ]
    assert "Cloud" not in message


def test_every_rejected_file_is_named(local_only):
    detail = unsupported_file_type_detail(
        ["sheet.xlsx", "mail.eml"], DocumentParser.LOCAL
    )
    assert "sheet.xlsx" in detail["message"]
    assert "mail.eml" in detail["message"]


# --- a format no parser opens -------------------------------------------


def test_a_format_nothing_reads_is_told_what_is_read_instead(cloud_too):
    message = unreadable_file_message("mail.eml")
    assert "mail.eml" in message
    assert ".pdf" in message
    assert "Markdown" in message
    # The formats only the retired unstructured parser opened are offered
    # nowhere, because nothing here opens them.
    _, _, offered = message.partition("It reads ")
    for ext in (".eml", ".msg", ".p7s", ".heic"):
        assert ext not in offered, ext
