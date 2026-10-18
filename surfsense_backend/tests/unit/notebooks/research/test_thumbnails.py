"""Result images are served through a signed proxy on our own origin."""

from __future__ import annotations

import httpx
import pytest
from fastapi import FastAPI

from app.notebooks.research.schemas import ResultRow
from app.notebooks.research.thumbnails import route
from app.notebooks.research.thumbnails.fetch_image import (
    FetchedImage,
    ImageRefusedError,
    fetch_image,
)
from app.notebooks.research.thumbnails.links import proxied, with_proxied_images
from app.notebooks.research.thumbnails.signing import decode_url

pytestmark = pytest.mark.unit

IMAGE = "https://i.ytimg.com/vi/abc/hqdefault.jpg"


def _row(source="youtube", thumbnail=IMAGE, url="https://www.youtube.com/watch?v=abc"):
    return ResultRow(
        id="1", url=url, title="t", snippet="", source=source, thumbnail_url=thumbnail
    )


def _params(link: str) -> dict[str, str]:
    return dict(httpx.URL(link).params)


def test_rows_carry_a_signed_same_origin_link_not_the_remote_url():
    row = with_proxied_images(_row())
    assert row.thumbnail_url.startswith("/api/v1/research/thumbnails?")
    assert IMAGE not in row.thumbnail_url
    assert decode_url(_params(row.thumbnail_url)["u"]) == IMAGE
    assert row.favicon_url is None


def test_web_rows_get_a_favicon_and_keep_no_thumbnail():
    row = with_proxied_images(
        _row(source="web", thumbnail=None, url="https://nhs.uk/a")
    )
    assert row.thumbnail_url is None
    assert "nhs.uk" in decode_url(_params(row.favicon_url)["u"])


def test_non_http_images_are_dropped():
    assert proxied("data:image/png;base64,AAAA") is None
    assert proxied("javascript:alert(1)") is None


@pytest.fixture
def client():
    app = FastAPI()
    app.include_router(route.router, prefix="/api/v1")
    fetched = []

    async def fake_fetch(url):
        fetched.append(url)
        if "missing" in url:
            raise ImageRefusedError("status 404")
        return FetchedImage(b"\x89PNG....", "image/png")

    app.dependency_overrides[route.get_image_fetcher] = lambda: fake_fetch
    transport = httpx.ASGITransport(app=app)
    return httpx.AsyncClient(transport=transport, base_url="http://test"), fetched


async def test_a_signed_link_is_served_with_safe_headers(client):
    http, fetched = client
    response = await http.get(proxied(IMAGE))
    assert response.status_code == 200
    assert response.headers["content-type"] == "image/png"
    assert response.headers["x-content-type-options"] == "nosniff"
    assert "sandbox" in response.headers["content-security-policy"]
    assert "max-age=86400" in response.headers["cache-control"]
    assert fetched == [IMAGE]


async def test_an_unsigned_or_tampered_link_fetches_nothing(client):
    http, fetched = client
    link = proxied(IMAGE)
    other = proxied("http://169.254.169.254/latest/meta-data")
    tampered = link.split("&s=")[0] + "&s=" + _params(other)["s"]
    assert (await http.get(tampered)).status_code == 404
    assert (await http.get("/api/v1/research/thumbnails?u=aGk&s=x")).status_code == 404
    assert fetched == []


async def test_a_refused_image_is_a_short_lived_404(client):
    http, _ = client
    response = await http.get(proxied("https://cdn.example/missing.jpg"))
    assert response.status_code == 404
    assert "max-age=300" in response.headers["cache-control"]


async def test_private_addresses_are_never_fetched():
    with pytest.raises(ImageRefusedError):
        await fetch_image("http://127.0.0.1:8000/secret.png")


async def test_svg_and_html_are_refused(monkeypatch):
    monkeypatch.setattr(
        "app.notebooks.research.thumbnails.fetch_image.is_publicly_routable",
        lambda url: True,
    )
    for kind in ("image/svg+xml", "text/html"):
        transport = httpx.MockTransport(
            lambda request, kind=kind: httpx.Response(
                200, headers={"content-type": kind}, content=b"<svg/>"
            )
        )
        with pytest.raises(ImageRefusedError):
            await fetch_image("https://cdn.example/a", transport=transport)


async def test_redirects_are_checked_hop_by_hop(monkeypatch):
    checked = []

    def routable(url):
        checked.append(url)
        return "cdn.example" in url

    monkeypatch.setattr(
        "app.notebooks.research.thumbnails.fetch_image.is_publicly_routable", routable
    )
    transport = httpx.MockTransport(
        lambda request: httpx.Response(
            302, headers={"location": "http://10.0.0.5/x.png"}
        )
    )
    with pytest.raises(ImageRefusedError):
        await fetch_image("https://cdn.example/a.png", transport=transport)
    assert checked == ["https://cdn.example/a.png", "http://10.0.0.5/x.png"]
