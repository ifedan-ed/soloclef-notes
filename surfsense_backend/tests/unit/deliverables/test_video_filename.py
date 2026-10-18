"""A downloaded video is named after its title, not the render's working path."""

import pytest

from app.deliverables.video.executor import video_filename

pytestmark = pytest.mark.unit


@pytest.mark.parametrize(
    ("title", "expected"),
    [
        (
            "IQ, Motivation and Academic Performance",
            "IQ, Motivation and Academic Performance.mp4",
        ),
        ("Video Overview: Kawasaki / KD?", "Video Overview Kawasaki KD.mp4"),
        ("Écoute — 心臓", "Écoute — 心臓.mp4"),
        ("  ...  ", "video-62.mp4"),
        (None, "video-62.mp4"),
    ],
)
def test_the_file_is_named_after_the_video(title, expected):
    assert video_filename(title, 62) == expected


def test_a_long_title_is_trimmed():
    assert len(video_filename("x" * 400, 1)) <= 124
