from __future__ import annotations

import inspect
from collections import defaultdict
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import TypeVar

from vigiadev.domain.states import GlobalState, ServiceState


@dataclass(frozen=True, slots=True)
class Event:
    timestamp: datetime = field(default_factory=lambda: datetime.now(timezone.utc))


@dataclass(frozen=True, slots=True)
class PhaseChanged(Event):
    state: GlobalState = GlobalState.PLANNING


@dataclass(frozen=True, slots=True)
class ServiceStatusChanged(Event):
    service: str = ""
    state: ServiceState = ServiceState.DEFINED
    detail: str = ""


@dataclass(frozen=True, slots=True)
class HealthcheckCompleted(Event):
    service: str = ""
    success: bool = False
    latency_ms: float = 0.0
    detail: str = ""


@dataclass(frozen=True, slots=True)
class LogReceived(Event):
    service: str = ""
    stream: str = "stdout"
    message: str = ""


@dataclass(frozen=True, slots=True)
class PortConflictDetected(Event):
    service: str = ""
    port: int = 0
    pid: int | None = None
    policy: str = ""


@dataclass(frozen=True, slots=True)
class TaskCompleted(Event):
    task: str = ""
    exit_code: int = 0


@dataclass(frozen=True, slots=True)
class ProcessExited(Event):
    service: str = ""
    exit_code: int = 0


@dataclass(frozen=True, slots=True)
class ShutdownStarted(Event):
    reason: str = ""


EventHandler = Callable[[Event], Awaitable[None] | None]
E = TypeVar("E", bound=Event)


class EventBus:
    """In-process async bus shared by presentation adapters."""

    def __init__(self) -> None:
        self._subscribers: dict[type[Event], list[EventHandler]] = defaultdict(list)

    def subscribe(self, event_type: type[E], handler: EventHandler) -> Callable[[], None]:
        self._subscribers[event_type].append(handler)

        def unsubscribe() -> None:
            subscribers = self._subscribers[event_type]
            if handler in subscribers:
                subscribers.remove(handler)

        return unsubscribe

    async def publish(self, event: Event) -> None:
        handlers: list[EventHandler] = []
        for event_type, subscribers in self._subscribers.items():
            if isinstance(event, event_type):
                handlers.extend(subscribers)
        for handler in tuple(handlers):
            result = handler(event)
            if inspect.isawaitable(result):
                await result
