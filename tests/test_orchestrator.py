from __future__ import annotations

import asyncio
import socket
import sys
from pathlib import Path

import pytest

from vigiadev.adapters.health import HealthChecker
from vigiadev.adapters.port_resolver import PortResolver
from vigiadev.adapters.session_manifest import SessionManifest
from vigiadev.application.orchestrator import Orchestrator
from vigiadev.application.supervisor import ProcessSupervisor
from vigiadev.domain.events import EventBus, LogReceived, PhaseChanged, PortRemapped
from vigiadev.domain.models import (
    HealthCheckConfig,
    HealthType,
    PortPolicy,
    ServiceConfig,
    VigiaConfig,
)
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


def _orchestrator_for_service(
    tmp_path: Path,
    bus: EventBus,
    service: ServiceConfig,
) -> Orchestrator:
    return Orchestrator(
        VigiaConfig(project_name="remap-test", services={"api": service}),
        tmp_path,
        bus,
        ProcessSupervisor(bus),
        None,
        PortResolver(),
        HealthChecker(),
        SessionManifest(tmp_path),
    )


@pytest.mark.asyncio
async def test_port_remap_interpolation(tmp_path: Path) -> None:
    bus = EventBus()
    remapped: list[PortRemapped] = []
    bus.subscribe(PortRemapped, remapped.append)

    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        original_port = listener.getsockname()[1]
        listener.listen()
        service = ServiceConfig(
            command=[sys.executable, "-m", "http.server", "{port}"],
            env={"APP_URL": "http://127.0.0.1:{port}/api"},
            ports=[original_port],
            port_policy=PortPolicy.REMAP,
            healthcheck=HealthCheckConfig(
                type=HealthType.HTTP,
                url="http://127.0.0.1:{port}/health",
            ),
        )
        orchestrator = _orchestrator_for_service(tmp_path, bus, service)

        assert await orchestrator._resolve_ports("api", service) is False

    assert len(remapped) == 1
    target_port = remapped[0].target_port
    assert remapped[0].original_port == original_port
    assert original_port < target_port <= original_port + 50
    assert service.command[-1] == str(target_port)
    assert service.env["APP_URL"] == f"http://127.0.0.1:{target_port}/api"
    assert service.healthcheck is not None
    assert service.healthcheck.url == f"http://127.0.0.1:{target_port}/health"
    assert service.ports == [target_port]
    assert orchestrator.effective_ports["api"] == [target_port]
    assert orchestrator.port_remappings["api"] == {original_port: target_port}


@pytest.mark.asyncio
async def test_port_remap_healthcheck(tmp_path: Path) -> None:
    bus = EventBus()
    warnings: list[LogReceived] = []
    bus.subscribe(LogReceived, warnings.append)

    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        original_port = listener.getsockname()[1]
        listener.listen()
        service = ServiceConfig(
            command=[sys.executable, "-c", "pass"],
            ports=[original_port],
            port_policy=PortPolicy.REMAP,
            healthcheck=HealthCheckConfig(
                type=HealthType.TCP,
                port=original_port,
            ),
        )
        orchestrator = _orchestrator_for_service(tmp_path, bus, service)

        assert await orchestrator._resolve_ports("api", service) is False

    assert service.healthcheck is not None
    assert service.healthcheck.port == service.ports[0]
    assert service.healthcheck.port != original_port
    assert len(warnings) == 1
    assert "{port} is absent from command and env" in warnings[0].message
