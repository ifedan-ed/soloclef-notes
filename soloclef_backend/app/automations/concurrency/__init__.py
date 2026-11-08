"""The "If already running" setting (``execution.concurrency``).

``admission`` decides whether a fire creates a run, ``start`` whether a
created run may start now, ``handoff`` wakes the next queued run when one
ends; ``lock`` is the row lock all three decide under. Import the submodules
directly; this root stays empty to keep the import graph acyclic.
"""

from __future__ import annotations
