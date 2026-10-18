"""From what a caller asked for to the parser that reads the file.

Old clients still send ``processing_mode=basic|premium``; new ones send
``parser=local|cloud``. A chosen parser is honoured strictly; with no
choice (connector syncs) the default reads what it can and the other
parser picks up the rest.
"""

import pytest

from app.etl_pipeline.etl_document import DocumentParser
from app.etl_pipeline.exceptions import (
    EtlServiceUnavailableError,
    EtlUnsupportedFileError,
)
from app.etl_pipeline.parser_selection import (
    ParserUnavailableError,
    automatically_routable_extensions,
    default_parser,
    files_the_parser_cannot_read,
    parse_parser_choice,
    resolve_upload_parser,
    route_document,
    upload_parser_from_task,
)

pytestmark = pytest.mark.unit


@pytest.fixture
def deployment(monkeypatch):
    def configure(*, cloud="LLAMACLOUD", api_key="key", default="LOCAL"):
        monkeypatch.setattr("app.config.config.ETL_CLOUD_SERVICE", cloud)
        monkeypatch.setattr("app.config.config.LLAMA_CLOUD_API_KEY", api_key)
        monkeypatch.setattr("app.config.config.ETL_DEFAULT_PARSER", default)
        monkeypatch.setattr("app.config.config.AZURE_DI_ENDPOINT", None)
        monkeypatch.setattr("app.config.config.AZURE_DI_KEY", None)

    configure()
    return configure


@pytest.mark.parametrize(
    ("value", "expected"),
    [
        ("local", DocumentParser.LOCAL),
        ("CLOUD", DocumentParser.CLOUD),
        (" basic ", DocumentParser.LOCAL),
        ("premium", DocumentParser.CLOUD),
        ("Premium", DocumentParser.CLOUD),
        ("enterprise", None),
        ("", None),
        (None, None),
    ],
)
def test_legacy_modes_map_onto_the_new_choice(value, expected):
    assert parse_parser_choice(value) is expected


def test_default_parser_is_local_unless_configured(deployment):
    assert default_parser() is DocumentParser.LOCAL
    deployment(default="cloud")
    assert default_parser() is DocumentParser.CLOUD
    deployment(default="nonsense")
    assert default_parser() is DocumentParser.LOCAL


def test_parser_wins_over_the_legacy_processing_mode(deployment):
    assert resolve_upload_parser("local", "premium") is DocumentParser.LOCAL
    assert resolve_upload_parser("cloud", "basic") is DocumentParser.CLOUD


def test_old_clients_are_mapped_basic_to_local_and_premium_to_cloud(deployment):
    assert resolve_upload_parser(None, "basic") is DocumentParser.LOCAL
    assert resolve_upload_parser(None, "premium") is DocumentParser.CLOUD


def test_no_choice_takes_the_server_default(deployment):
    deployment(default="CLOUD")
    assert resolve_upload_parser(None, None) is DocumentParser.CLOUD


def test_an_explicit_cloud_choice_that_cannot_run_is_refused(deployment):
    deployment(api_key=None)
    with pytest.raises(ParserUnavailableError) as raised:
        resolve_upload_parser("cloud", None)
    assert raised.value.capability.unavailable_reason == "missing_api_key"


def test_a_legacy_premium_that_cannot_run_degrades_to_the_default(deployment):
    deployment(api_key=None)
    assert resolve_upload_parser(None, "premium") is DocumentParser.LOCAL


def test_queued_tasks_from_before_the_split_still_resolve(deployment):
    assert upload_parser_from_task("premium") is DocumentParser.CLOUD
    assert upload_parser_from_task("basic") is DocumentParser.LOCAL
    assert upload_parser_from_task("cloud") is DocumentParser.CLOUD
    assert upload_parser_from_task(None) is DocumentParser.LOCAL


def test_a_chosen_parser_is_honoured_strictly(deployment):
    assert route_document("a.pdf", DocumentParser.LOCAL).service == "PYMUPDF4LLM"
    assert route_document("a.pdf", DocumentParser.CLOUD).service == "LLAMACLOUD"
    # MuPDF drops every string cell of a spreadsheet: Local refuses rather
    # than quietly sending the file to the metered service.
    with pytest.raises(EtlUnsupportedFileError, match="local parser"):
        route_document("sheet.xlsx", DocumentParser.LOCAL)


def test_a_chosen_cloud_parser_that_cannot_run_is_an_error(deployment):
    deployment(api_key=None)
    with pytest.raises(EtlServiceUnavailableError, match="missing_api_key"):
        route_document("a.pdf", DocumentParser.CLOUD)


def test_no_choice_prefers_the_default_and_falls_back_for_what_it_cannot_read(
    deployment,
):
    assert route_document("a.pdf", None).parser is DocumentParser.LOCAL
    assert route_document("sheet.xlsx", None).parser is DocumentParser.CLOUD
    with pytest.raises(EtlUnsupportedFileError):
        route_document("mail.p7s", None)


def test_no_choice_never_falls_back_to_an_unavailable_cloud(deployment):
    deployment(api_key=None)
    with pytest.raises(EtlUnsupportedFileError):
        route_document("sheet.xlsx", None)
    assert ".xlsx" not in automatically_routable_extensions()
    assert ".pdf" in automatically_routable_extensions()


def test_upload_check_names_files_the_chosen_parser_cannot_read(deployment):
    names = ["a.pdf", "notes.md", "data.csv", "talk.mp3", "sheet.xlsx", "x.exe"]
    assert files_the_parser_cannot_read(
        names, DocumentParser.LOCAL, vision_requested=False
    ) == ["sheet.xlsx", "x.exe"]
    assert files_the_parser_cannot_read(
        names, DocumentParser.CLOUD, vision_requested=False
    ) == ["x.exe"]


def test_upload_check_lets_images_through_when_vision_reads_them(deployment):
    # MuPDF cannot open WebP; the vision LLM can.
    assert files_the_parser_cannot_read(
        ["photo.webp"], DocumentParser.LOCAL, vision_requested=False
    ) == ["photo.webp"]
    assert (
        files_the_parser_cannot_read(
            ["photo.webp"], DocumentParser.LOCAL, vision_requested=True
        )
        == []
    )
