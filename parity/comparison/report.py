"""What the run prints.

A passing line is one line, because nobody reads them. A failing line prints
every dimension that differed and nothing that did not, because the first thing
anyone asks is "which part".
"""

from __future__ import annotations


class Report:
    def __init__(self) -> None:
        self.asked = 0
        self.diverged_cases = 0
        self.failures: list[str] = []

    def heading(self, route: str, file_name: str, count: int) -> None:
        print(f"\n{route}  ({file_name}, {count} cases)")

    def identical(self, label: str, status: int, length: int) -> None:
        self.asked += 1
        print(f"  ok   {label}  [{status} {length}B]")

    def diverged(self, route: str, label: str, differences: list[tuple[str, str, str]]) -> None:
        self.asked += 1
        self.diverged_cases += 1
        self.failures.append(f"{route} · {label}")
        print(f"  FAIL {label}")
        for dimension, python, go in differences:
            print(f"       {dimension}:")
            print(f"         python: {python}")
            print(f"         go:     {go}")

    def missing_routes(self, routes: list[str]) -> None:
        for route in routes:
            self.failures.append(f"no case asks {route}")
            print(f"\nFAIL no case asks {route}")

    def summary(self) -> int:
        print(f"\n{self.asked - self.diverged_cases}/{self.asked} identical")
        if not self.failures:
            print("the two tiers answered identically")
            return 0
        print(f"{len(self.failures)} divergence(s):")
        for failure in self.failures:
            print(f"  - {failure}")
        return 1
