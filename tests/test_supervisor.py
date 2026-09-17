from __future__ import annotations

import asyncio
import os
import signal
import sys
from pathlib import Path

import pytest

from vigiadev.application.supervisor import ProcessSupervisor
from vigiadev.domain.events import EventBus, LogReceived


@pytest.mark.asyncio
async def test_spawn_uses_own_posix_group_and_stops_with_sigterm(tmp_path: Path) -> None:
    bus = EventBus()
    supervisor = ProcessSupervisor(bus)
    record = await supervisor.start(
        "worker",
        [sys.executable, "-c", "import time; time.sleep(30)"],
        tmp_path,
    )
    assert record.pid == record.pgid
    assert os.getpgid(record.pid) == record.pgid

    await supervisor.stop("worker", timeout=1.0)
    assert record.process.returncode == -signal.SIGTERM
    assert supervisor.processes == {}


@pytest.mark.asyncio
async def test_supervisor_streams_real_child_output(tmp_path: Path) -> None:
    bus = EventBus()
    messages: list[str] = []

    def collect(event: LogReceived) -> None:
        messages.append(event.message)

    bus.subscribe(LogReceived, collect)
    supervisor = ProcessSupervisor(bus)
    await supervisor.start(
        "echo",
        [sys.executable, "-c", "print('child-output', flush=True)"],
        tmp_path,
    )
    assert await supervisor.wait("echo") == 0
    assert messages == ["child-output"]

