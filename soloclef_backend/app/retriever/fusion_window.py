"""How many chunks rank fusion scores before the context budget is spent.

The budget can only spend what fusion ranked. A window sized to the documents
asked for leaves the prompt short of the passages *around* the answer: those
are neighbouring chunks, they fuse well, and a window of a few dozen stops
before them. So the window is its own setting, independent of ``top_k``, and
is read here by every search that spends a character budget.
"""

from __future__ import annotations


def configured_fusion_candidate_chunks() -> int:
    """Chunks each search fuses, refused rather than guessed if unusable."""
    from app.config import config

    candidates = int(config.RETRIEVAL_FUSION_CANDIDATE_CHUNKS)
    if candidates < 1:
        raise ValueError(
            f"RETRIEVAL_FUSION_CANDIDATE_CHUNKS={candidates} must be at least 1"
        )
    return candidates


__all__ = ["configured_fusion_candidate_chunks"]
