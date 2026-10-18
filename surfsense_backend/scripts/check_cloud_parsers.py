"""Parse one small public-domain page through each cloud parser, once.

    docker exec -w /app soloclef-backend-1 python scripts/check_cloud_parsers.py
    docker exec -w /app soloclef-backend-1 python scripts/check_cloud_parsers.py llamacloud

Before switching ETL_CLOUD_SERVICE, this shows whether the other service
reads a document with the key this server holds, and how long it takes. It
writes a one-page PDF (the first paragraph of the US Declaration of
Independence) to a temporary folder, sends it through the backend's own
parser functions, with one attempt each so a failure is not resubmitted and
billed again, and prints the time, the length and the first lines. It reads
no one's documents, writes nothing to the database and prints no key. Each
run costs one page at each service it asks (Datalab balanced: $0.004;
LlamaCloud agentic: 10 credits or more).
"""

from __future__ import annotations

import argparse
import asyncio
import os
import sys
import tempfile
import time

SERVICES = ("datalab", "llamacloud")
FIRST_WORDS = "When in the Course of human events"
TEXT = (
    "When in the Course of human events, it becomes necessary for one people "
    "to dissolve the political bands which have connected them with another, "
    "and to assume among the powers of the earth, the separate and equal "
    "station to which the Laws of Nature and of Nature's God entitle them, a "
    "decent respect to the opinions of mankind requires that they should "
    "declare the causes which impel them to the separation."
)


def write_sample_pdf(folder: str) -> str:
    import pymupdf

    path = os.path.join(folder, "declaration.pdf")
    document = pymupdf.open()
    page = document.new_page()
    page.insert_text((72, 72), "The Declaration of Independence", fontsize=16)
    page.insert_textbox(pymupdf.Rect(72, 100, 540, 400), TEXT, fontsize=11)
    document.save(path)
    document.close()
    return path


async def _parse(service: str, path: str) -> str:
    if service == "datalab":
        from app.etl_pipeline.parsers.datalab import parse_with_datalab

        return await parse_with_datalab(path, "declaration.pdf", 1, max_retries=1)
    from app.etl_pipeline.parsers.llamacloud import parse_with_llamacloud

    return await parse_with_llamacloud(path, 1, max_retries=1)


async def check(service: str, path: str) -> bool:
    """Parse ``path`` with ``service`` and print the outcome; True if it read it."""
    started = time.monotonic()
    try:
        markdown = await _parse(service, path)
    except Exception as exc:
        print(
            f"{service.upper()}: FAILED after {time.monotonic() - started:.1f}s: "
            f"{type(exc).__name__}: {str(exc)[:300]}"
        )
        return False
    elapsed = time.monotonic() - started
    text = str(markdown)
    pages = getattr(markdown, "page_texts", None) or ()
    print(
        f"{service.upper()}: read in {elapsed:.1f}s, {len(text)} characters, "
        f"{len(pages)} page(s), first words found: {FIRST_WORDS in text}"
    )
    for line in [line for line in text.splitlines() if line.strip()][:3]:
        print(f"    | {line[:120]}")
    return FIRST_WORDS in text


async def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("services", nargs="*", choices=SERVICES, metavar="service")
    args = parser.parse_args(argv)

    from app.config import config

    print(
        f"This server: ETL_CLOUD_SERVICE={config.ETL_CLOUD_SERVICE}, "
        f"ETL_DEFAULT_PARSER={config.ETL_DEFAULT_PARSER}, "
        f"DATALAB_API_KEY {'set' if config.DATALAB_API_KEY else 'unset'}, "
        f"LLAMA_CLOUD_API_KEY {'set' if config.LLAMA_CLOUD_API_KEY else 'unset'}"
    )
    with tempfile.TemporaryDirectory(prefix="parser-check-") as folder:
        path = write_sample_pdf(folder)
        results = [await check(service, path) for service in args.services or SERVICES]
    return 0 if all(results) else 1


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
