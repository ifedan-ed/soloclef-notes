"""Authorship conventions for recorded revisions.

Author = whose content change it is; committer = who recorded it.
"""

from __future__ import annotations

#: Committer of every agent-recorded revision; author of autonomous writes.
AGENT_IDENTITY = "Soloclef Agent <agent@soloclef>"

#: Author of the one-time seed revision that migrates a workspace into the store.
MIGRATION_IDENTITY = "Soloclef Migration <migration@soloclef>"


def user_identity(user_id: str | None) -> str:
    """Revision author for a user action; autonomous actions author as the agent."""
    if user_id is None:
        return AGENT_IDENTITY
    return f"Soloclef User <{user_id}@users.soloclef>"
