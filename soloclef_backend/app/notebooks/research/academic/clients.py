"""The network edges of the academic sources, bundled so tests can replace them."""

from __future__ import annotations

from dataclasses import dataclass

from .polite_http import FetchText, polite_get_text
from .scholar.search import FetchScholarPage, fetch_scholar_page


@dataclass(frozen=True)
class AcademicClients:
    fetch_text: FetchText = polite_get_text
    fetch_scholar_page: FetchScholarPage = fetch_scholar_page


DEFAULT_CLIENTS = AcademicClients()
