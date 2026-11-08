"""Import the app's real job.py and protocol.py without app/__init__ chains.

app.config pulls the ML stack in, so the two modules under test are loaded by
file path under stub parent packages.
"""

import importlib.util
import pathlib
import sys
import types

BACKEND = str(pathlib.Path(__file__).resolve().parents[2] / "soloclef_backend")


def _load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    sys.modules[name] = module
    spec.loader.exec_module(module)
    return module


def dedupe_keys():
    """The app's own app/mail_queue/dedupe/keys.py.

    It reaches queue_store.py for the queue's key prefix, and queue_store
    imports the Redis client at module level, so the client -- not anything
    under test -- is stood in for.
    """
    for name in ("redis", "redis.asyncio"):
        sys.modules.setdefault(name, types.ModuleType(name))
    sys.modules["redis"].asyncio = sys.modules["redis.asyncio"]
    sys.modules["redis.asyncio"].Redis = object
    for parent in ("app", "app.mail_queue", "app.mail_queue.dedupe"):
        sys.modules.setdefault(parent, types.ModuleType(parent))
    _load("app.mail_queue.queue_store", f"{BACKEND}/app/mail_queue/queue_store.py")
    return _load(
        "app.mail_queue.dedupe.keys", f"{BACKEND}/app/mail_queue/dedupe/keys.py"
    )


def real_modules():
    for parent in ("app", "app.mailer", "app.mail_queue"):
        sys.modules.setdefault(parent, types.ModuleType(parent))
    protocol = _load("app.mailer.protocol", f"{BACKEND}/app/mailer/protocol.py")
    sys.modules["app.mailer"].protocol = protocol
    job = _load("app.mail_queue.job", f"{BACKEND}/app/mail_queue/job.py")
    return protocol, job
