from __future__ import annotations

from rich.console import Console

from vigiadev.domain.events import (
    Event,
    EventBus,
    HealthcheckCompleted,
    LogReceived,
    PhaseChanged,
    PortConflictDetected,
    PortRemapped,
    ProcessExited,
    ServiceStatusChanged,
    ShutdownStarted,
    TaskCompleted,
)


class StreamView:
    """Flat EventBus consumer suitable for CI and redirected terminals."""

    def __init__(self, event_bus: EventBus, console: Console | None = None) -> None:
        self.console = console or Console()
        self._unsubscribe = event_bus.subscribe(Event, self.handle)

    def close(self) -> None:
        self._unsubscribe()

    def handle(self, event: Event) -> None:
        timestamp = event.timestamp.astimezone().strftime("%H:%M:%S")
        if isinstance(event, LogReceived):
            color = "red" if event.stream == "stderr" else "cyan"
            self.console.print(
                f"[dim]{timestamp}[/] [{color}]{event.service}[/] {event.message}"
            )
        elif isinstance(event, PhaseChanged):
            self.console.print(f"[bold blue]{timestamp} vigiaDev[/] {event.state.value}")
        elif isinstance(event, ServiceStatusChanged):
            color = {
                "ready": "green",
                "reused": "green",
                "failed": "red",
                "checking": "yellow",
            }.get(event.state.value, "blue")
            detail = f" — {event.detail}" if event.detail else ""
            self.console.print(
                f"[dim]{timestamp}[/] [{color}]{event.service}: {event.state.value}[/]{detail}"
            )
        elif isinstance(event, HealthcheckCompleted):
            color = "green" if event.success else "red"
            self.console.print(
                f"[dim]{timestamp}[/] [{color}]health {event.service} "
                f"{event.latency_ms:.1f}ms[/] — {event.detail}"
            )
        elif isinstance(event, PortConflictDetected):
            self.console.print(
                f"[yellow]{timestamp} port {event.port} occupied for {event.service} "
                f"(pid={event.pid or 'unknown'}, policy={event.policy})[/]"
            )
        elif isinstance(event, PortRemapped):
            self.console.print(
                f"[yellow]{timestamp} {event.service}: {event.original_port} "
                f"➔ {event.target_port} [REMAP][/]"
            )
        elif isinstance(event, TaskCompleted):
            self.console.print(
                f"[green]{timestamp} task {event.task} completed ({event.exit_code})[/]"
            )
        elif isinstance(event, ProcessExited):
            self.console.print(
                f"[dim]{timestamp} process {event.service} exited ({event.exit_code})[/]"
            )
        elif isinstance(event, ShutdownStarted):
            self.console.print(f"[yellow]{timestamp} shutdown: {event.reason}[/]")

