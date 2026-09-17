from __future__ import annotations

import asyncio
import sys
from pathlib import Path

import pytest
from textual.widgets import RichLog, Static, TabbedContent

from vigiadev.adapters.health import HealthChecker
from vigiadev.adapters.port_resolver import PortResolver
from vigiadev.adapters.session_manifest import SessionManifest
from vigiadev.application.orchestrator import Orchestrator
from vigiadev.application.supervisor import ProcessSupervisor
from vigiadev.cli import _run_tui
from vigiadev.domain.events import (
    EventBus,
    HealthcheckCompleted,
    LogReceived,
    PhaseChanged,
    ServiceStatusChanged,
)
from vigiadev.domain.states import GlobalState, ServiceState
from vigiadev.domain.models import ServiceConfig, VigiaConfig
from vigiadev.presentation.textual_app import VigiaDevApp


async def _noop_stop() -> None:
    return None


async def _noop_restart(name: str) -> None:
    del name


class MountAwareOrchestrator:
    def __init__(self, app: VigiaDevApp) -> None:
        self.app = app
        self.started_after_mount = False
        self.stop_calls: list[str] = []

    async def run(self) -> None:
        self.started_after_mount = (
            self.app.ready_event.is_set() and self.app._mounted_ready
        )
        self.app.exit()

    async def stop(self, reason: str) -> None:
        self.stop_calls.append(reason)


class EarlyExitApp:
    def __init__(self) -> None:
        self.ready_event = asyncio.Event()

    async def run_async(self) -> None:
        return None


class MustNotStartOrchestrator:
    def __init__(self) -> None:
        self.run_calls = 0

    async def run(self) -> None:
        self.run_calls += 1

    async def stop(self, reason: str) -> None:
        del reason


@pytest.mark.asyncio
async def test_textual_tabs_status_and_shortcuts() -> None:
    bus = EventBus()
    stopped: list[bool] = []
    restarted: list[str] = []

    async def stop() -> None:
        stopped.append(True)

    async def restart(name: str) -> None:
        restarted.append(name)

    app = VigiaDevApp(bus, {"api": [8000]}, stop, restart)
    async with app.run_test(size=(100, 30)) as pilot:
        await bus.publish(
            ServiceStatusChanged(service="api", state=ServiceState.READY)
        )
        await bus.publish(
            HealthcheckCompleted(service="api", success=True, latency_ms=12.5)
        )
        await bus.publish(LogReceived(service="api", message="ready"))
        status = str(app.query_one("#status", Static).render())
        assert "api" in status
        assert "8000" in status
        assert "12.5ms" in status

        app.query_one("#logs", TabbedContent).active = "service-0"
        await pilot.pause()
        await pilot.press("r")
        await pilot.press("c")
        await pilot.press("q")

    assert restarted == ["api"]
    assert stopped == [True]


@pytest.mark.asyncio
async def test_events_before_mount_are_buffered_until_ready() -> None:
    bus = EventBus()
    app = VigiaDevApp(bus, {"api": [8000]}, _noop_stop, _noop_restart)
    early_log = LogReceived(service="api", message="early")

    assert app.ready_event.is_set() is False
    await bus.publish(ServiceStatusChanged(service="api", state=ServiceState.STARTING))
    await bus.publish(early_log)
    assert app.service_states["api"] is ServiceState.STARTING
    assert app._pending_logs == [early_log]

    async with app.run_test(size=(100, 30)):
        assert app.ready_event.is_set() is True
        assert app._mounted_ready is True
        assert app._pending_logs == []
        status = str(app.query_one("#status", Static).render())
        assert "starting" in status

    assert app._mounted_ready is False


@pytest.mark.asyncio
async def test_dom_queries_are_defensive_during_mount_transition() -> None:
    bus = EventBus()
    app = VigiaDevApp(bus, {"api": [8000]}, _noop_stop, _noop_restart)
    transitional_log = LogReceived(service="api", message="transition")

    async with app.run_test(size=(100, 30)):
        await app.query_one("#status", Static).remove()
        app._render_status()

        await app.query_one("#log-all", RichLog).remove()
        app._write_log(transitional_log)

        assert app._pending_logs == [transitional_log]


@pytest.mark.asyncio
async def test_run_tui_starts_orchestrator_only_after_mount() -> None:
    bus = EventBus()
    app = VigiaDevApp(bus, {"api": [8000]}, _noop_stop, _noop_restart)
    orchestrator = MountAwareOrchestrator(app)

    await _run_tui(app, orchestrator)  # type: ignore[arg-type]

    assert orchestrator.started_after_mount is True
    assert app.ready_event.is_set() is True
    assert app._mounted_ready is False


@pytest.mark.asyncio
async def test_run_tui_exits_cleanly_if_app_finishes_before_mount() -> None:
    app = EarlyExitApp()
    orchestrator = MustNotStartOrchestrator()

    await _run_tui(app, orchestrator)  # type: ignore[arg-type]

    assert app.ready_event.is_set() is False
    assert orchestrator.run_calls == 0


@pytest.mark.asyncio
async def test_run_tui_with_real_orchestrator_has_no_mount_race(
    tmp_path: Path,
) -> None:
    bus = EventBus()
    manifest = SessionManifest(tmp_path)
    orchestrator = Orchestrator(
        VigiaConfig(
            project_name="tui-race",
            services={
                "api": ServiceConfig(
                    command=[
                        sys.executable,
                        "-c",
                        "import signal; print('ready', flush=True); signal.pause()",
                    ]
                )
            },
        ),
        tmp_path,
        bus,
        ProcessSupervisor(bus),
        None,
        PortResolver(),
        HealthChecker(),
        manifest,
    )
    app = VigiaDevApp(
        bus,
        {"api": []},
        lambda: orchestrator.stop("TUI quit"),
        orchestrator.restart_service,
    )

    def exit_when_running(event: PhaseChanged) -> None:
        if event.state is GlobalState.RUNNING:
            app.exit()

    bus.subscribe(PhaseChanged, exit_when_running)

    await asyncio.wait_for(_run_tui(app, orchestrator), timeout=3)

    assert orchestrator.state is GlobalState.STOPPED
    assert app.ready_event.is_set() is True
    assert app._mounted_ready is False
    assert not manifest.manifest_path.exists()
    assert not manifest.lock_path.exists()
