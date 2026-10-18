"""No value in the backend names one server.

Every deployment runs this code, so a host, mail domain or sign-in name
written into it would be inherited by servers that are not that one. Those
come from each server's own configuration (``app.brand.site_url_from_env``,
``SUPPORT_EMAIL``, ``BTCPAY_URL``, ``NEXTCLOUD_BASE_URL``, ``OIDC_*``).
Docstrings and comments may describe a deployment; values may not.
"""

from __future__ import annotations

import ast
import re
from pathlib import Path

import pytest

pytestmark = pytest.mark.unit

BACKEND = Path(__file__).resolve().parents[2]
ONE_SERVER = re.compile(r"example|soloclef\.com", re.IGNORECASE)
DATA_FILES = ("*.md", "*.yaml", "*.yml", "*.json", "*.txt", "*.html", "*.jinja2")


def _docstrings(tree: ast.AST) -> set[int]:
    owners = (ast.Module, ast.ClassDef, ast.FunctionDef, ast.AsyncFunctionDef)
    found = set()
    for node in ast.walk(tree):
        if isinstance(node, owners) and node.body:
            first = node.body[0]
            if isinstance(first, ast.Expr) and isinstance(first.value, ast.Constant):
                found.add(id(first.value))
    return found


def _string_values(path: Path) -> list[str]:
    tree = ast.parse(path.read_text(), filename=str(path))
    skip = _docstrings(tree)
    return [
        node.value
        for node in ast.walk(tree)
        if isinstance(node, ast.Constant)
        and isinstance(node.value, str)
        and id(node) not in skip
    ]


def test_no_string_in_the_code_names_one_server() -> None:
    sources = [*(BACKEND / "app").rglob("*.py"), *(BACKEND / "alembic").rglob("*.py")]
    assert len(sources) > 500, "the scan found the source tree"
    named = [
        f"{path.relative_to(BACKEND)}: {value!r}"
        for path in sources
        for value in _string_values(path)
        if ONE_SERVER.search(value)
    ]
    assert named == []


def test_no_data_file_names_one_server() -> None:
    files = [p for pattern in DATA_FILES for p in (BACKEND / "app").rglob(pattern)]
    named = [
        str(p.relative_to(BACKEND))
        for p in files
        if ONE_SERVER.search(p.read_text(errors="replace"))
    ]
    assert named == []
