"""docker/pgbouncer/render-config.sh turns the environment into pgbouncer's config.

The pooler's credentials must never be in the repository, so the config is
rendered at container start: the template is committed, the password arrives
through the environment and lands only in the userlist.
"""

from __future__ import annotations

import os
import stat
import subprocess
from pathlib import Path

import pytest

pytestmark = pytest.mark.unit

_REPO = Path(__file__).resolve().parents[4]
_RENDERER = _REPO / "docker/pgbouncer/render-config.sh"
_TEMPLATE = _RENDERER.with_name("pgbouncer.ini.template")

_PASSWORD = "render-test-password"  # pragma: allowlist secret


def render(tmp_path: Path, **overrides: str) -> subprocess.CompletedProcess[str]:
    environment = {
        "PATH": os.environ["PATH"],
        "PGBOUNCER_RENDER_ONLY": "1",
        "PGBOUNCER_TEMPLATE": str(_TEMPLATE),
        "PGBOUNCER_RUNTIME_DIR": str(tmp_path / "runtime"),
        "PGBOUNCER_PORT": "6432",
        "DB_HOST": "soloclef-db-1",
        "DB_PORT": "5432",
        "DB_NAME": "notes",
        "DB_USER": "notes_app",
        "DB_PASSWORD": _PASSWORD,
    }
    environment.update(overrides)
    return subprocess.run(
        ["/bin/sh", str(_RENDERER)],
        env={name: value for name, value in environment.items() if value != ""},
        capture_output=True,
        text=True,
        check=False,
    )


def test_the_rendered_config_names_this_stack_s_database_and_pools_per_transaction(
    tmp_path,
):
    result = render(tmp_path)

    assert result.returncode == 0, result.stderr
    rendered = (tmp_path / "runtime/pgbouncer.ini").read_text()
    assert "notes = host=soloclef-db-1 port=5432 dbname=notes" in rendered
    assert "pool_mode = transaction" in rendered
    assert "listen_port = 6432" in rendered
    assert f"auth_file = {tmp_path}/runtime/userlist.txt" in rendered
    assert "stats_users = notes_app" in rendered
    # Nothing is left for pgbouncer to choke on, and no secret is in the file
    # the deploy notes tell people to cat.
    assert "@" not in rendered.replace("@...@", "")
    assert _PASSWORD not in rendered


def test_the_userlist_holds_the_password_for_scram_and_nobody_else_can_read_it(
    tmp_path,
):
    result = render(tmp_path)

    assert result.returncode == 0, result.stderr
    userlist = tmp_path / "runtime/userlist.txt"
    assert userlist.read_text() == f'"notes_app" "{_PASSWORD}"\n'
    assert stat.S_IMODE(userlist.stat().st_mode) == 0o600


def test_rendering_twice_does_not_append_a_second_user(tmp_path):
    render(tmp_path)
    render(tmp_path)

    userlist = tmp_path / "runtime/userlist.txt"
    assert userlist.read_text().count("notes_app") == 1


@pytest.mark.parametrize(
    "missing", ["DB_HOST", "DB_PORT", "DB_NAME", "DB_USER", "DB_PASSWORD"]
)
def test_a_missing_setting_stops_the_pooler_instead_of_guessing(tmp_path, missing):
    result = render(tmp_path, **{missing: ""})

    assert result.returncode != 0
    assert missing in result.stderr
    assert _PASSWORD not in result.stderr + result.stdout
    assert not (tmp_path / "runtime/pgbouncer.ini").exists()


def test_a_missing_template_stops_the_pooler(tmp_path):
    result = render(tmp_path, PGBOUNCER_TEMPLATE=str(tmp_path / "absent.ini.template"))

    assert result.returncode != 0
    assert "template" in result.stderr
