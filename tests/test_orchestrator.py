from __future__ import annotations

import asyncio
import sys
from pathlib import Path

import pytest

from vigiadev.adapters.health import HealthChecker
from vigiadev.adapters.port_resolver import PortResolver
from vigiadev.adapters.session_manifest import SessionManifest
from vigiadev.application.orchestrator import Orchestrator
from vigiadev.application.supervisor import ProcessSupervisor
from vigiadev.domain.events import EventBus, PhaseChanged
from vigiadev.domain.models import ServiceConfig, VigiaConfig
from vigiadev.domain.states import GlobalState


@pytest.mark.asyncio
async def test_requested_stop_is_clean_not_a_process_failure(tmp_path: Path) -> None:
    bus = EventBus()
    running = asyncio.Event()

    def observe(event: PhaseChanged) -> None:
        if event.state is GlobalState.RUNNING:
            running.set()

    bus.subscribe(PhaseChanged, observe)
    supervisor = ProcessSupervisor(bus)
    manifest = SessionManifest(tmp_path)
    orchestrator = Orchestrator(
        VigiaConfig(
            project_name="shutdown-test",
            services={
                "worker": ServiceConfig(
                    command=[sys.executable, "-c", "import time; time.sleep(30)"]
                )
            },
        ),
        tmp_path,
        bus,
        supervisor,
        None,
        PortResolver(),
        HealthChecker(),
        manifest,
    )

    run_task = asyncio.create_task(orchestrator.run())
    await asyncio.wait_for(running.wait(), timeout=2)
    await orchestrator.stop("test requested")
    await asyncio.wait_for(run_task, timeout=2)

    assert orchestrator.state is GlobalState.STOPPED
    assert supervisor.processes == {}
    assert not manifest.manifest_path.exists()
    assert not manifest.lock_path.exists()
