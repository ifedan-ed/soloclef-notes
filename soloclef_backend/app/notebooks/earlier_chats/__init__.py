"""Chats from before notebooks: each workspace keeps them in one notebook.

The standalone chat UI is gone, so a chat that was started outside a notebook
(before notebooks existed, or since by a messaging channel or a copied public
chat) is filed in the workspace's "Earlier chats" notebook, where it opens in
the notebook's chat switcher like any other.
"""

from .model import NotebookEarlierChat
from .placement import EARLIER_CHATS_ICON, EARLIER_CHATS_TITLE, place_thread

__all__ = [
    "EARLIER_CHATS_ICON",
    "EARLIER_CHATS_TITLE",
    "NotebookEarlierChat",
    "place_thread",
]
