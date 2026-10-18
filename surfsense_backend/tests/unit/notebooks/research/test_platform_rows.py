"""Each platform's scraper output reads back as result rows the Sources panel can show."""

from __future__ import annotations

import pytest

from app.capabilities.amazon.scrape.schemas import ScrapeOutput as AmazonOutput
from app.capabilities.google_maps.scrape.schemas import ScrapeOutput as MapsOutput
from app.capabilities.google_search.scrape.schemas import ScrapeOutput as SerpOutput
from app.capabilities.indeed.scrape.schemas import ScrapeOutput as IndeedOutput
from app.capabilities.instagram.scrape.schemas import ScrapeOutput as InstagramOutput
from app.capabilities.reddit.scrape.schemas import ScrapeOutput as RedditOutput
from app.capabilities.tiktok.scrape.schemas import ScrapeOutput as TikTokOutput
from app.capabilities.walmart.scrape.schemas import ScrapeOutput as WalmartOutput
from app.capabilities.youtube.scrape.schemas import ScrapeOutput as YouTubeOutput
from app.notebooks.research.sources.registry import PLANS, plan_for
from app.notebooks.research.urls import result_id

pytestmark = pytest.mark.unit


def _rows(source, output):
    return plan_for(source).to_rows(output, source)


def test_every_source_has_a_plan_whose_input_validates():
    for source, plan in PLANS.items():
        payload = plan.build_input("solar panels", 25)
        assert payload.estimated_units >= 1, source


def test_google_serp_organic_results_become_rows():
    output = SerpOutput.model_validate(
        {
            "items": [
                {
                    "organicResults": [
                        {
                            "title": "Solar <b>basics</b>",
                            "url": "https://www.energy.gov/solar",
                            "description": "How **panels** work.",
                            "date": "Mar 3, 2025",
                        },
                        {"title": "No link", "url": None},
                        {"title": "FTP", "url": "ftp://example.com/x"},
                    ],
                    "paidResults": [{"title": "Ad", "url": "https://ads.example"}],
                }
            ]
        }
    )
    [row] = _rows("google_search", output)
    assert row.url == "https://www.energy.gov/solar"
    assert row.title == "Solar basics"
    assert row.snippet == "How panels work."
    assert row.meta == "energy.gov · Mar 3, 2025"
    assert row.source == "google_search"
    assert row.id == result_id("https://energy.gov/solar/")


def test_youtube_video_shows_duration_and_views():
    output = YouTubeOutput.model_validate(
        {
            "items": [
                {
                    "title": "Solar explained",
                    "url": "https://www.youtube.com/watch?v=abc",
                    "viewCount": 1_234_567,
                    "duration": "12:31",
                    "thumbnailUrl": "https://i.ytimg.com/vi/abc/hq.jpg",
                    "text": "A video about panels",
                    "channelName": "Energy Now",
                }
            ]
        }
    )
    [row] = _rows("youtube", output)
    assert row.meta == "12:31 · 1.2M views · Energy Now"
    assert row.thumbnail_url == "https://i.ytimg.com/vi/abc/hq.jpg"
    assert row.snippet == "A video about panels"


def test_reddit_keeps_posts_and_drops_comments():
    output = RedditOutput.model_validate(
        {
            "items": [
                {
                    "dataType": "post",
                    "url": "https://www.reddit.com/r/solar/comments/1/x/",
                    "title": "Is solar worth it?",
                    "body": "Thinking about it",
                    "parsedCommunityName": "solar",
                    "upVotes": 120,
                    "numberOfComments": 45,
                },
                {
                    "dataType": "comment",
                    "url": "https://www.reddit.com/r/solar/comments/1/x/c1",
                    "body": "yes",
                },
            ]
        }
    )
    [row] = _rows("reddit", output)
    assert row.meta == "r/solar · 120 upvotes · 45 comments"
    assert row.title == "Is solar worth it?"


def test_instagram_post_titled_by_first_caption_line():
    output = InstagramOutput.model_validate(
        {
            "items": [
                {
                    "url": "https://www.instagram.com/p/XYZ/",
                    "caption": "New install!\n#solar #green",
                    "ownerUsername": "sunco",
                    "likesCount": 2500,
                    "displayUrl": "https://cdn.instagram.com/x.jpg",
                },
                {"url": "https://www.instagram.com/p/ERR/", "error": "not_found"},
            ]
        }
    )
    [row] = _rows("instagram", output)
    assert row.title == "New install!"
    assert row.meta == "@sunco · 2.5K likes"
    assert row.thumbnail_url == "https://cdn.instagram.com/x.jpg"


def test_tiktok_video_and_error_items():
    output = TikTokOutput.model_validate(
        {
            "items": [
                {
                    "webVideoUrl": "https://www.tiktok.com/@sun/video/1",
                    "text": "Panels on my roof",
                    "authorMeta": {"name": "sun"},
                    "videoMeta": {"duration": 75, "coverUrl": "https://p16.tiktok/x"},
                    "playCount": 9800,
                },
                {"errorCode": "blocked", "url": "https://www.tiktok.com/search?q=x"},
            ]
        }
    )
    [row] = _rows("tiktok", output)
    assert row.meta == "@sun · 1:15 · 9.8K views"
    assert row.title == "Panels on my roof"


def test_tiktok_runs_a_short_query_as_its_joined_hashtag_first():
    # Anonymous TikTok search is login-gated; hashtag feeds still load.
    payload = plan_for("tiktok").build_input("solar roof", 10)
    assert payload.urls == []
    assert payload.hashtags == ["solarroof", "solar"]
    assert payload.max_items == 10
    assert payload.results_per_page == 5


def test_tiktok_long_query_uses_its_longest_distinctive_words():
    payload = plan_for("tiktok").build_input(
        "what is bronchiolitis high flow nasal cannula", 10
    )
    assert payload.hashtags == ["bronchiolitis", "cannula"]


def test_tiktok_single_word_query_is_one_hashtag():
    payload = plan_for("tiktok").build_input("Bronchiolitis", 10)
    assert payload.hashtags == ["bronchiolitis"]
    assert payload.results_per_page == 10


def test_google_maps_place_shows_rating_and_address():
    output = MapsOutput.model_validate(
        {
            "items": [
                {
                    "title": "Sun Installers",
                    "url": "https://www.google.com/maps/place/?q=place_id:ChIJ1",
                    "address": "1 Main St, Denver",
                    "categoryName": "Solar contractor",
                    "totalScore": 4.5,
                    "reviewsCount": 88,
                }
            ]
        }
    )
    [row] = _rows("google_maps", output)
    assert row.meta == "Solar contractor · 4.5★ (88)"
    assert row.snippet == "1 Main St, Denver"


def test_indeed_job_shows_company_location_and_pay():
    output = IndeedOutput.model_validate(
        {
            "items": [
                {
                    "title": "Solar Technician",
                    "jobUrl": "https://www.indeed.com/viewjob?jk=1",
                    "company": "SunCo",
                    "formattedLocation": "Austin, TX",
                    "salary": {"salaryText": "$25 an hour"},
                    "descriptionText": "Install panels.",
                }
            ]
        }
    )
    [row] = _rows("indeed", output)
    assert row.meta == "SunCo · Austin, TX · $25 an hour"


@pytest.mark.parametrize(
    ("source", "output_type", "title_field"),
    [("amazon", AmazonOutput, "title"), ("walmart", WalmartOutput, "name")],
)
def test_products_show_price_and_stars(source, output_type, title_field):
    output = output_type.model_validate(
        {
            "items": [
                {
                    title_field: "Solar charger",
                    "url": f"https://www.{source}.com/p/1",
                    "price": {"value": 19.99, "currency": "USD"},
                    "stars": 4.5,
                    "thumbnailImage": "https://img.example/1.jpg",
                },
                {"url": f"https://www.{source}.com/p/2", "error": "blocked"},
            ]
        }
    )
    [row] = _rows(source, output)
    assert row.meta == "$19.99 · 4.5★"
    assert row.title == "Solar charger"
