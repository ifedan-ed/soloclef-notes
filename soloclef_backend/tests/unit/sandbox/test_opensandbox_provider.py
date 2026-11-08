import time
from datetime import timedelta
from types import SimpleNamespace
from unittest.mock import AsyncMock

import pytest
from opensandbox.exceptions import SandboxApiException

from app.config import config as app_config
from app.sandbox.providers.opensandbox import OpenSandboxProvider, OpenSandboxSession


class _Files:
    def __init__(self, exc: Exception) -> None:
        self.exc = exc

    async def read_bytes(self, _path: str) -> bytes:
        raise self.exc


def _session(exc: Exception) -> OpenSandboxSession:
    sandbox = SimpleNamespace(id="sandbox-1", files=_Files(exc))
    return OpenSandboxSession(sandbox, ttl_seconds=900)


def test_provider_uses_the_shared_sandbox_operation_budget(monkeypatch) -> None:
    monkeypatch.setattr(app_config, "SANDBOX_OPERATION_TIMEOUT_SECONDS", 37)

    provider = OpenSandboxProvider()

    assert provider._config.request_timeout == timedelta(seconds=37)


async def test_read_file_normalizes_provider_404() -> None:
    session = _session(
        SandboxApiException(
            "provider URL and request id",
            status_code=404,
            request_id="secret-request",
        )
    )

    with pytest.raises(FileNotFoundError) as raised:
        await session.read_file("/workspace/missing.pdf")

    assert str(raised.value) == "/workspace/missing.pdf"
    assert "secret-request" not in str(raised.value)


async def test_read_file_hides_other_provider_details() -> None:
    session = _session(
        SandboxApiException(
            "provider URL and request id",
            status_code=500,
            request_id="secret-request",
        )
    )

    with pytest.raises(RuntimeError, match="Sandbox read failed") as raised:
        await session.read_file("/workspace/report.pdf")

    assert "secret-request" not in str(raised.value)


@pytest.mark.parametrize(
    ("status_code", "error_type", "message"),
    [
        (403, PermissionError, "Sandbox read was denied"),
        (504, TimeoutError, "Sandbox read timed out"),
    ],
)
async def test_read_file_normalizes_permission_and_timeout_failures(
    status_code: int, error_type: type[Exception], message: str
) -> None:
    session = _session(SandboxApiException("provider detail", status_code=status_code))

    with pytest.raises(error_type, match=message):
        await session.read_file("/workspace/report.pdf")


async def test_a_long_command_keeps_its_sandbox_alive() -> None:
    """A full video render outlived the 15-minute TTL as one command, and its
    sandbox expired under it (job 59: "Could not connect to the backend
    sandbox"). While a command runs, the session renews on a timer."""
    import asyncio

    renewals: list[float] = []
    loop = asyncio.get_running_loop()
    sandbox = SimpleNamespace(
        id="sbx",
        renew=AsyncMock(side_effect=lambda _d: renewals.append(loop.time())),
    )

    async def slow_command(_command):
        await asyncio.sleep(0.35)
        return SimpleNamespace(error=None, exit_code=0)

    sandbox.commands = SimpleNamespace(run=slow_command)
    session = OpenSandboxSession(sandbox, ttl_seconds=0.3)
    session._last_renewed = time.monotonic()  # fresh, so no renewal on entry

    result = await session.run_command("node render.mjs props.json out.mp4")

    assert result.exit_code == 0
    assert len(renewals) >= 2  # every ttl/3 while the command runs


async def test_the_keep_alive_stops_when_the_command_ends() -> None:
    import asyncio

    sandbox = SimpleNamespace(id="sbx", renew=AsyncMock())

    async def quick_command(_command):
        return SimpleNamespace(error=None, exit_code=0)

    sandbox.commands = SimpleNamespace(run=quick_command)
    session = OpenSandboxSession(sandbox, ttl_seconds=900)
    session._last_renewed = time.monotonic()
    await session.run_command("true")
    await asyncio.sleep(0)
    assert not [
        t for t in asyncio.all_tasks() if "renew_periodically" in repr(t.get_coro())
    ]


def test_sandboxes_are_created_with_the_configured_cpu_and_memory() -> None:
    """A 1-CPU, 2 GiB sandbox rendered video at 6.8 fps; the limits are
    settings now (SANDBOX_CPU, SANDBOX_MEMORY), defaulting to 6 CPUs, 6 GiB."""
    import inspect

    from app.sandbox.providers import opensandbox as provider

    source = inspect.getsource(provider)
    assert '"cpu": app_config.SANDBOX_CPU' in source
    assert '"memory": app_config.SANDBOX_MEMORY' in source
    assert app_config.SANDBOX_CPU == "6"
    assert app_config.SANDBOX_MEMORY == "6Gi"
    assert app_config.VIDEO_SANDBOX_RENDER_CONCURRENCY == 6
