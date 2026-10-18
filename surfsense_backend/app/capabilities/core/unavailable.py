"""A verb that cannot run here right now, said in words the person can act on.

Raised by an executor whose provider is reached with the workspace's own key
(Exa, Tavily, Browserbase) when the key is missing or refused, the provider
is out of credit, or it did not answer. The agent door records the run as an
error and hands the message to the model as the tool result, so the turn goes
on and the answer can say what happened, instead of the whole delegation
failing. Nothing is charged for such a run.
"""

from __future__ import annotations


class CapabilityUnavailableError(Exception):
    def __init__(self, message: str) -> None:
        super().__init__(message)
        self.message = message
