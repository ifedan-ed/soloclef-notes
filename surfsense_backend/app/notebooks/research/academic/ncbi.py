"""E-utilities URLs carrying the identity NCBI's usage policy asks for.

``tool`` always, ``email`` and ``api_key`` only when configured: nothing about
the owner is sent unless they chose to set it.
"""

from __future__ import annotations

from collections.abc import Mapping

from app.config import config

from .polite_http import build_url

EUTILS = "https://eutils.ncbi.nlm.nih.gov/entrez/eutils/"


def eutils_url(endpoint: str, params: Mapping[str, object]) -> str:
    identity: dict[str, object] = {"tool": config.NCBI_TOOL}
    if config.NCBI_EMAIL:
        identity["email"] = config.NCBI_EMAIL
    if config.NCBI_API_KEY:
        identity["api_key"] = config.NCBI_API_KEY
    return build_url(f"{EUTILS}{endpoint}.fcgi", {**params, **identity})
