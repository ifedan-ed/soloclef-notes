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


def real_modules():
    for parent in ("app", "app.mailer", "app.mail_queue"):
        sys.modules.setdefault(parent, types.ModuleType(parent))
    protocol = _load("app.mailer.protocol", f"{BACKEND}/app/mailer/protocol.py")
    sys.modules["app.mailer"].protocol = protocol
    job = _load("app.mail_queue.job", f"{BACKEND}/app/mail_queue/job.py")
    return protocol, job
