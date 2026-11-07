"""How this app's engines connect: directly to Postgres, or through pgbouncer.

Nothing here opens a connection. The engines live in ``app.db`` (the API) and
``app.tasks.celery_tasks`` (the workers); this package holds the driver
settings both of them need, so the pooler is configured in one place.
"""
