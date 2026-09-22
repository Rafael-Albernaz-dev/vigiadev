from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable, Iterable, Mapping

from rich.text import Text
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Vertical
from textual.css.query import NoMatches
from textual.widgets import Footer, Header, RichLog, Static, TabbedContent, TabPane

from vigiadev.domain.events import (
    Event,
    EventBus,
    HealthcheckCompleted,
    LogReceived,
    PhaseChanged,
    PortRemapped,
    ServiceStatusChanged,
)
from vigiadev.domain.states import ServiceState


StopCallback = Callable[[], Awaitable[None]]
RestartCallback = Callable[[str], Awaitable[None]]


class VigiaDevApp(App[None]):
    CSS = """
    Screen { layout: vertical; }
    #status { height: auto; min-height: 3; padding: 0 1; border: round $primary; }
    #logs { height: 1fr; }
    RichLog { padding: 0 1; }
    """
    BINDINGS = [
        Binding("q", "quit_safe", "Sair"),
        Binding("r", "restart_focused", "Reiniciar"),
        Binding("c", "clear_logs", "Limpar logs"),
    ]

    def __init__(
        self,
        event_bus: EventBus,
        services: Mapping[str, Iterable[int]] | Iterable[str],
        stop_callback: StopCallback,
        restart_callback: RestartCallback,
    ) -> None:
        super().__init__()
        if isinstance(services, Mapping):
            self.service_ports = {
                name: tuple(ports) for name, ports in services.items()
            }
        else:
            self.service_ports = {name: () for name in services}
        self.services = tuple(self.service_ports)
        self.stop_callback = stop_callback
        self.restart_callback = restart_callback
        self.service_states = {name: ServiceState.DEFINED for name in self.services}
        self.service_latency: dict[str, float | None] = {
            name: None for name in self.services
        }
        self.port_remappings: dict[str, list[tuple[int, int]]] = {
            name: [] for name in self.services
        }
        self._tab_ids = {
            name: f"service-{index}" for index, name in enumerate(self.services)
        }
        self._log_ids = {
            name: f"log-{index}" for index, name in enumerate(self.services)
        }
        self._service_by_tab = {value: key for key, value in self._tab_ids.items()}
        self._pending_logs: list[LogReceived] = []
        self.ready_event = asyncio.Event()
        self._mounted_ready = False
        self._unsubscribe = event_bus.subscribe(Event, self.handle_event)

    def compose(self) -> ComposeResult:
        yield Header()
        with Vertical():
            yield Static("vigiaDev: planning", id="status")
            with TabbedContent(id="logs"):
                with TabPane("ALL", id="service-all"):
                    yield RichLog(id="log-all")
                for name in self.services:
                    with TabPane(name, id=self._tab_ids[name]):
                        yield RichLog(id=self._log_ids[name])
        yield Footer()

    def on_mount(self) -> None:
        self._mounted_ready = True
        self._render_status()
        pending, self._pending_logs = self._pending_logs, []
        for event in pending:
            self._write_log(event)
        self.ready_event.set()

    def on_unmount(self) -> None:
        self._mounted_ready = False
        self._unsubscribe()

    async def handle_event(self, event: Event) -> None:
        if isinstance(event, ServiceStatusChanged):
            self.service_states[event.service] = event.state
            self._render_status()
        elif isinstance(event, PhaseChanged) and self._mounted_ready:
            self.title = f"vigiaDev — {event.state.value}"
        elif isinstance(event, HealthcheckCompleted):
            self.service_latency[event.service] = event.latency_ms
            if self._mounted_ready:
                self.sub_title = f"{event.service}: {event.latency_ms:.1f}ms"
            self._render_status()
        elif isinstance(event, PortRemapped):
            self.port_remappings.setdefault(event.service, []).append(
                (event.original_port, event.target_port)
            )
            ports = self.service_ports.get(event.service, ())
            self.service_ports[event.service] = tuple(
                event.target_port if port == event.original_port else port
                for port in ports
            )
            self._render_status()
        elif isinstance(event, LogReceived):
            self._write_log(event)

    async def action_quit_safe(self) -> None:
        await self.stop_callback()
        self.exit()

    async def action_restart_focused(self) -> None:
        tabs = self.query_one("#logs", TabbedContent)
        service = self._service_by_tab.get(tabs.active or "")
        if service is None:
            self.notify("Selecione uma aba de serviço para reiniciar", severity="warning")
            return
        try:
            await self.restart_callback(service)
        except Exception as error:  # UI boundary: render domain/application error.
            self.notify(str(error), severity="error")

    def action_clear_logs(self) -> None:
        tabs = self.query_one("#logs", TabbedContent)
        active = tabs.active or "service-all"
        if active == "service-all":
            self.query_one("#log-all", RichLog).clear()
            return
        service = self._service_by_tab.get(active)
        if service is not None:
            self.query_one(f"#{self._log_ids[service]}", RichLog).clear()

    def _render_status(self) -> None:
        if not self._mounted_ready:
            return
        icons = {
            ServiceState.READY: ("●", "green"),
            ServiceState.REUSED: ("●", "green"),
            ServiceState.FAILED: ("●", "red"),
            ServiceState.CHECKING: ("●", "yellow"),
        }
        text = Text()
        for index, name in enumerate(self.services):
            if index:
                text.append("   ")
            state = self.service_states[name]
            icon, color = icons.get(state, ("●", "blue"))
            text.append(f"{icon} {name} ", style=color)
            text.append(state.value, style="dim")
            details: list[str] = []
            details.extend(
                f"{original} ➔ {target} [REMAP]"
                for original, target in self.port_remappings.get(name, [])
            )
            if ports := self.service_ports[name]:
                details.append("ports " + ",".join(str(port) for port in ports))
            if (latency := self.service_latency[name]) is not None:
                details.append(f"{latency:.1f}ms")
            if details:
                text.append(f" ({'; '.join(details)})", style="dim")
        try:
            status = self.query_one("#status", Static)
        except NoMatches:
            return
        status.update(text)

    def _write_log(self, event: LogReceived) -> None:
        if not self._mounted_ready:
            self._pending_logs.append(event)
            return
        color = "red" if event.stream == "stderr" else "cyan"
        combined = Text()
        combined.append(f"[{event.service}] ", style=color)
        combined.append(event.message)
        try:
            all_log = self.query_one("#log-all", RichLog)
            service_log = (
                self.query_one(f"#{self._log_ids[event.service]}", RichLog)
                if event.service in self._tab_ids
                else None
            )
        except NoMatches:
            self._pending_logs.append(event)
            return
        all_log.write(combined)
        if service_log is not None:
            service_log.write(event.message)

