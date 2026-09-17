from __future__ import annotations

from collections.abc import Sequence
from dataclasses import dataclass
from pathlib import Path
from typing import Protocol

from vigiadev.domain.models import HealthCheckConfig, PortPolicy


@dataclass(frozen=True, slots=True)
class PortInspection:
    port: int
    occupied: bool
    pid: int | None = None


@dataclass(frozen=True, slots=True)
class HealthResult:
    success: bool
    latency_ms: float
    detail: str
    attempts: int = 1


class ComposeGateway(Protocol):
    project_name: str

    async def up(self, services: Sequence[str]) -> None: ...

    async def restart(self, service: str) -> None: ...

    async def down(self) -> None: ...

    async def container_names(self) -> list[str]: ...


class PortGateway(Protocol):
    def inspect(self, port: int, host: str = "127.0.0.1") -> PortInspection: ...

    def decision(self, inspection: PortInspection, policy: PortPolicy, healthy: bool) -> str: ...

    async def terminate_listener(self, inspection: PortInspection, timeout: float = 5.0) -> None: ...


class HealthGateway(Protocol):
    async def wait(self, config: HealthCheckConfig, cwd: Path, env: dict[str, str]) -> HealthResult: ...


class ManifestGateway(Protocol):
    run_id: str

    def acquire(self) -> None: ...

    def create(self, compose_project: str | None, compose_file: Path | None) -> None: ...

    def add_process(self, name: str, pid: int, pgid: int, started_at: float) -> None: ...

    def remove_process(self, name: str) -> None: ...

    def set_containers(self, names: Sequence[str]) -> None: ...

    def cleanup(self) -> None: ...

