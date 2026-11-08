"""Shared wording for product listings (Amazon and Walmart)."""

from __future__ import annotations

from ..snippets import join_meta

_SYMBOLS = {"USD": "$", "EUR": "€", "GBP": "£", "CAD": "CA$", "AUD": "A$"}


def price_text(price) -> str | None:
    if price is None or price.value is None:
        return None
    symbol = _SYMBOLS.get((price.currency or "USD").upper())
    amount = f"{price.value:,.2f}"
    return f"{symbol}{amount}" if symbol else f"{amount} {price.currency}"


def product_meta(price, stars: float | None) -> str | None:
    return join_meta(price_text(price), f"{stars:g}★" if stars else None)
