"""Automation run executor: plan walker, step dispatch, retries, persistence.

Import the submodules directly (``runtime.executor.execute_run``); this
package root stays empty so the "If already running" rules in
``app.automations.concurrency`` can use the run repository without an import
cycle through the executor.
"""

from __future__ import annotations
