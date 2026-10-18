"""Push notifications for the phone app (the plan's §6, backend item B9).

The app registers its Expo push token (``POST /api/v1/push/devices``) and
keeps per-kind choices on the server (``/api/v1/push/preferences``). When
something finishes while nobody is looking, the rest of the app calls one
function in ``events`` (fire-and-forget, never raising); a Celery task looks
up the person's devices and choices, sends through Expo's push service in
batches, and keeps the tickets so a later task can read the receipts and
drop the tokens of phones that uninstalled the app.

What a lock screen shows is generic ("Your audio overview is ready"): never
an answer's text, and no notebook or announcement title unless the owner
turns ``PUSH_INCLUDE_TITLES`` on (decision 9). The notification carries only
an in-app path; the app opens it. Limits: ``custom/docs/push-notifications.md``.
"""
