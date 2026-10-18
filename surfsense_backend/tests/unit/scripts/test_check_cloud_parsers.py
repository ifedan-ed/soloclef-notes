"""The cloud parser check reads its own sample page, once per service.

The real services are replaced by mocked HTTP transports: one service reads
the page, the other refuses it, and the check says which, without a key.
"""

from __future__ import annotations

import functools

import httpx
import pytest

from app.etl_pipeline.parsers import datalab, llamacloud
from scripts import check_cloud_parsers

pytestmark = pytest.mark.unit


def _datalab_reading_the_page(request: httpx.Request) -> httpx.Response:
    if request.method == "POST":
        assert b"%PDF" in request.content
        return httpx.Response(
            200, json={"success": True, "request_check_url": "https://d.test/r/1"}
        )
    text = "# The Declaration of Independence\n\n" + check_cloud_parsers.TEXT
    return httpx.Response(
        200, json={"status": "complete", "success": True, "markdown": text}
    )


def _llamacloud_out_of_credits(request: httpx.Request) -> httpx.Response:
    return httpx.Response(
        402, json={"detail": "You've exceeded the maximum number of credits."}
    )


@pytest.fixture
def services(monkeypatch):
    calls = {"datalab": 0, "llamacloud": 0}

    def counted(name, function):
        async def run(*args, **kwargs):
            calls[name] += 1
            return await function(*args, **kwargs)

        return run

    monkeypatch.setattr("app.config.config.DATALAB_API_KEY", "secret-datalab")
    monkeypatch.setattr("app.config.config.LLAMA_CLOUD_API_KEY", "secret-llama")
    monkeypatch.setattr(
        datalab,
        "parse_with_datalab",
        counted(
            "datalab",
            functools.partial(
                datalab.parse_with_datalab,
                transport=httpx.MockTransport(_datalab_reading_the_page),
                poll_interval=0,
            ),
        ),
    )
    monkeypatch.setattr(
        llamacloud,
        "parse_with_llamacloud",
        counted(
            "llamacloud",
            functools.partial(
                llamacloud.parse_with_llamacloud,
                transport=httpx.MockTransport(_llamacloud_out_of_credits),
            ),
        ),
    )
    return calls


async def test_reports_each_service_once_and_fails_when_one_cannot_read(
    services, capsys
):
    assert await check_cloud_parsers.main([]) == 1
    out = capsys.readouterr().out
    assert "DATALAB: read in" in out
    assert "first words found: True" in out
    assert "LLAMACLOUD: FAILED" in out
    assert "maximum number of credits" in out
    assert services == {"datalab": 1, "llamacloud": 1}
    assert "secret-" not in out


async def test_one_service_can_be_checked_alone(services, capsys):
    assert await check_cloud_parsers.main(["datalab"]) == 0
    assert services == {"datalab": 1, "llamacloud": 0}
    assert "LLAMACLOUD" not in capsys.readouterr().out.split("\n", 1)[1]
