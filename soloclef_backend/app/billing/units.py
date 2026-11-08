"""The one unit of account: micro-USD of retail value.

Every balance, price, grant and debit in billing is an integer count of
micro-USD (1_000_000 == $1.00), the same unit the wallet columns on ``user``
already used. A *credit* is only a display unit: 1 credit == 1 US cent of
retail value == 10_000 micros. Prices shown to people are whole credits, but
keeping micros underneath lets an admin price something at half a credit and
keeps the card and crypto amounts exact.
"""

from __future__ import annotations

MICROS_PER_CREDIT = 10_000
MICROS_PER_CENT = 10_000
MICROS_PER_DOLLAR = 1_000_000


def credits_to_micros(credits: float | int) -> int:
    return round(float(credits) * MICROS_PER_CREDIT)


def micros_to_credits(micros: int) -> float:
    """Credits with at most two decimals, for display and API payloads."""
    return round(int(micros) / MICROS_PER_CREDIT, 2)
