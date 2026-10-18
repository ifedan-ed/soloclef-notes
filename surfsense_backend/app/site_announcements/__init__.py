"""Site announcements: an administrator's message to every account or one workspace.

Each send is recorded once in ``site_announcements`` and delivered as one
ordinary ``notifications`` row per recipient (type ``site_announcement``), so
the inbox, its unread badge, per-person read state and Zero live sync all work
unchanged. Withdrawing deletes those rows; expiry hides them.
"""
