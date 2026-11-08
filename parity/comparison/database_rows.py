"""The rows a write left behind, and the reseed that lets both tiers be asked.

A write's answer is half the question. The other half is the database: a route
that answers identically and commits a different row has not been ported, and a
rollback is only observable here. So a case naming tables is asked of the same
seed twice — ask Python, read the rows, reseed; ask Go, read the rows, reseed —
and the two dumps are compared as text.

The reading and the reseeding are one `docker exec` rather than two, and the
reseed is done *after* a dump rather than before the next one, because a
comparison of fourteen hundred cases is a comparison of several thousand
round trips into a container and that is nearly all of its running time.
"""

from __future__ import annotations

import subprocess

# Printed between the dump and the seed's own output, which is how one psql
# invocation can do both and still be read.
_END_OF_DUMP = "---parity-end-of-dump---"


class Database:
    def __init__(self, container: str, user: str, name: str, seed_file: str):
        self._psql = [
            "docker", "exec", "--interactive", container, "psql",
            "--tuples-only", "--no-align", "--quiet",
            f"--username={user}", f"--dbname={name}",
        ]
        self._seed_file = seed_file
        # Whether the rows are known to be the seed's. up.sh seeds before the
        # first case, and every write case reseeds after reading.
        self.seeded = True

    def _run(self, arguments: list[str]) -> str:
        with open(self._seed_file, "rb") as seed:
            done = subprocess.run(self._psql + arguments, stdin=seed,
                                  capture_output=True)
        if done.returncode != 0:
            raise RuntimeError("psql failed: " + done.stderr.decode("utf-8", "replace"))
        return done.stdout.decode("utf-8", "replace")

    def _dump_arguments(self, tables: list[str]) -> list[str]:
        arguments = []
        for table in tables:
            # Ordered by the primary key so the dump is a function of the rows
            # and not of the order Postgres happened to return them in.
            arguments += ["--command", f"SELECT '{table}:'",
                          "--command", f"SELECT * FROM {table} ORDER BY id"]
        return arguments + ["--command", f"SELECT '{_END_OF_DUMP}'"]

    def reseed(self) -> None:
        self._run(["--file=/dev/stdin"])
        self.seeded = True

    def ensure_seeded(self) -> None:
        if not self.seeded:
            self.reseed()

    def dump_then_reseed(self, tables: list[str]) -> str:
        """The named tables' rows, and the seed put back in the same call."""
        read = self._run(self._dump_arguments(tables) + ["--file=/dev/stdin"])
        self.seeded = True
        return read.split(_END_OF_DUMP, 1)[0]
