"""Mask secrets in log text before it leaves the process.

Applied by the formatters to the fully rendered line (message, arguments and
traceback), so a secret interpolated anywhere — an exception carrying a DSN, a
dict repr with ``api_key``, a query string with ``?token=`` — is covered
without every call site having to remember.
"""

from __future__ import annotations

import re

MASK = "***"

# key=value / key: value / "key": "value" / 'key': 'value' where the key names
# a secret. The key must *end* in the secret word, so ``max_tokens=4096`` and
# ``prompt_tokens=12`` are left alone while ``access_token=...`` is masked.
_SECRET_KEY = (
    r"[A-Za-z0-9_.-]*?"
    r"(?:api[_-]?key|apikey|secret|password|passwd|pwd|token|cookie|"
    r"authorization|credentials?|private[_-]?key|session[_-]?id|signature|"
    r"x-api-key)"
)
_KV = re.compile(
    r"(?i)(?P<key>\b" + _SECRET_KEY + r")(?P<sep>[\"']?\s*[:=]\s*)"
    r"(?P<val>\"[^\"]*\"|'[^']*'|(?:bearer|basic|token)\s+[^\s\"',;&}]+|[^\s\"',;&}\]]+)"
)
# Header-style lines where the whole remainder is the secret.
_HEADER = re.compile(r"(?im)^(\s*(?:cookie|set-cookie|authorization)\s*:\s*).+$")
_BEARER = re.compile(r"(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}")
# Credentials inside URLs: scheme://user:password@host
_URL_CREDS = re.compile(r"(?P<pre>[a-z][a-z0-9+.-]*://[^\s:/@]+:)[^\s@/]+@", re.I)
# Well-known token shapes, wherever they appear.
_TOKEN_SHAPES = re.compile(
    r"\b(?:"
    r"sk-[A-Za-z0-9_-]{16,}"  # OpenAI / LiteLLM virtual keys
    r"|ss_pat_[A-Za-z0-9_-]{8,}"  # SurfSense personal access tokens
    r"|gh[pousr]_[A-Za-z0-9]{20,}"  # GitHub
    r"|xox[abpr]-[A-Za-z0-9-]{10,}"  # Slack
    r"|AKIA[0-9A-Z]{16}"  # AWS access key id
    r"|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}"  # JWT
    # Expo push tokens (app/push): whoever holds one can push to that phone.
    r"|Expo(?:nent)?PushToken\[[^\]\s]{1,250}\]"
    r")"
)


def _mask_kv(match: re.Match[str]) -> str:
    val = match.group("val")
    masked = f"{val[0]}{MASK}{val[0]}" if val[:1] in "\"'" else MASK
    return f"{match.group('key')}{match.group('sep')}{masked}"


def redact(text: str) -> str:
    """Return ``text`` with secrets masked. Cheap enough for every line."""
    if not text:
        return text
    text = _HEADER.sub(lambda m: m.group(1) + MASK, text)
    text = _URL_CREDS.sub(lambda m: m.group("pre") + MASK + "@", text)
    text = _BEARER.sub(lambda m: f"{m.group(1)} {MASK}", text)
    text = _KV.sub(_mask_kv, text)
    return _TOKEN_SHAPES.sub(MASK, text)
