"""Record the PDF page, or page range, each chunk came from.

``chunks.page_start`` / ``chunks.page_end`` are the 1-based inclusive pages of
the document's original PDF that a chunk's text was read from, so a citation
can open the original at the cited page. NULL means "page unknown": every row
that is not from a PDF, and every existing PDF row until
``scripts/backfill_chunk_pages.py`` has located it in the PDF's text layer.

Metadata-only ``ADD COLUMN`` (nullable, no default), so it does not rewrite the
chunks table. ``chunks`` is not in the Zero publication.

Numbered c0013 because c0012 is taken by a separate, not yet merged connectors
migration; whichever of the two merges second re-chains its ``down_revision``
to the other so there stays a single head.

Revision ID: c0013_chunk_source_pages
Revises: c0012_drop_removed_connectors
"""

from __future__ import annotations

from collections.abc import Sequence

from alembic import op

revision: str = "c0013_chunk_source_pages"
down_revision: str | None = "c0012_drop_removed_connectors"
branch_labels: str | Sequence[str] | None = None
depends_on: str | Sequence[str] | None = None


def upgrade() -> None:
    op.execute("ALTER TABLE chunks ADD COLUMN IF NOT EXISTS page_start INTEGER")
    op.execute("ALTER TABLE chunks ADD COLUMN IF NOT EXISTS page_end INTEGER")


def downgrade() -> None:
    op.execute("ALTER TABLE chunks DROP COLUMN IF EXISTS page_end")
    op.execute("ALTER TABLE chunks DROP COLUMN IF EXISTS page_start")
