"""Live, read-only public links to a whole notebook.

Unlike a chat snapshot, nothing is copied when the link is made: every request
reads the notebook as it is now, through ``scope.resolve_link``, which checks
the link, its expiry, its notebook and its creator's right to share before
anything else is read.
"""
