"""The embedded Studio section: escaped, marked, verifiable, and media by reference."""

from __future__ import annotations

import re

import pytest

from app.artifacts.verification.formats.html import check_html
from app.notebooks.studio.report_embeds.collect import (
    MediaEmbed,
    StudioEmbeds,
    StudyEmbed,
)
from app.notebooks.studio.report_embeds.labels import labels_for
from app.notebooks.studio.report_embeds.render import EMBED_MARKER, render_embeds

pytestmark = pytest.mark.unit

EN = labels_for(None)


def _full() -> StudioEmbeds:
    embeds = StudioEmbeds(
        mindmap=StudyEmbed(
            "Map",
            "# Iron\n- Absorption\n  - Heme\n  - Non-heme\n- Storage\n",
        ),
        flashcards=StudyEmbed(
            "Cards", [("<script>alert(1)</script>", "Back & forth", "a hint")]
        ),
        quiz=StudyEmbed(
            "Quiz",
            [("What stores iron?", ["Ferritin", "Insulin", "Urea", "Bile"], 0, "Why")],
        ),
        data_table=StudyEmbed("Table", [["Name", "Value"], ["Ferritin", "30"]]),
        audio=MediaEmbed("Talk", 11, 21, "audio/mpeg", 187),
        video=MediaEmbed("Film", 12, 22, "video/mp4", 487),
    )
    embeds.kinds = ["mindmap", "flashcards", "quiz", "data_table", "audio", "video"]
    return embeds


def test_nothing_to_embed_renders_nothing():
    assert render_embeds(StudioEmbeds(), EN) is None


def test_section_is_marked_and_passes_html_verification():
    html = render_embeds(_full(), EN)
    assert EMBED_MARKER in html and 'id="studio-outputs"' in html
    result = check_html(html.encode())
    assert result.findings == ()
    assert result.notes == ()


def test_content_is_escaped_not_executed():
    html = render_embeds(_full(), EN)
    assert "<script>alert(1)</script>" not in html
    assert "&lt;script&gt;alert(1)&lt;/script&gt;" in html
    assert "Back &amp; forth" in html
    # The only script is the section's own.
    assert html.count("<script>") == 1


def test_media_is_referenced_never_inlined():
    html = render_embeds(_full(), EN)
    players = re.findall(r"<(audio|video)\b[^>]*>", html)
    assert players == ["video", "audio"]
    assert 'data-surfsense-media="12:22"' in html
    assert 'data-surfsense-media="11:21"' in html
    assert not re.search(r"<(audio|video)\b[^>]*\ssrc=", html)
    assert "data:audio" not in html and "data:video" not in html


def test_mindmap_outline_nests_and_quiz_carries_its_answer():
    html = render_embeds(_full(), EN)
    assert re.search(
        r"<summary>Absorption</summary><ul><li class=\"ss-leaf\">Heme</li>", html
    )
    assert 'data-ss-answer="0"' in html
    assert "The answer is: Ferritin" in html


def test_missing_outputs_are_simply_absent():
    embeds = StudioEmbeds(quiz=_full().quiz, kinds=["quiz"])
    html = render_embeds(embeds, EN)
    assert "data-ss-quiz" in html
    assert "<audio" not in html and "<video" not in html and "<table" not in html


def test_labels_follow_the_report_language():
    html = render_embeds(_full(), labels_for("Spanish"))
    assert "Del Studio de este cuaderno" in html and "Mapa mental" in html
    assert labels_for("Klingon")["quiz"] == "Quiz"
