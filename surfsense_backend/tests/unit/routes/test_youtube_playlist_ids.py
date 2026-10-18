"""The playlist route reads YouTube's current playlist shape (no network).

YouTube replaced ``playlistVideoRenderer`` with ``lockupViewModel``; the route
found no videos in any playlist until it read the new shape (2026-10-09).
"""

from app.routes.youtube_routes import _extract_playlist_video_ids


def _lockup(content_id: str, content_type: str = "LOCKUP_CONTENT_TYPE_VIDEO") -> dict:
    return {"lockupViewModel": {"contentId": content_id, "contentType": content_type}}


def test_reads_lockup_view_models_in_order_without_repeats():
    browse = {
        "contents": {
            "sectionListRenderer": {
                "contents": [
                    {
                        "itemSectionRenderer": {
                            "contents": [
                                _lockup("GvgqDSnpRQM"),
                                _lockup("V4DDt30Aat4"),
                                _lockup("GvgqDSnpRQM"),
                            ]
                        }
                    },
                ]
            }
        },
        # The playlist's own sidebar lockup is not a video id (not 11 chars).
        "sidebar": _lockup("PLBCF2DAC6FFB574DE", "LOCKUP_CONTENT_TYPE_PLAYLIST"),
    }
    assert _extract_playlist_video_ids(browse) == ["GvgqDSnpRQM", "V4DDt30Aat4"]


def test_empty_response_gives_no_ids():
    assert _extract_playlist_video_ids({}) == []
