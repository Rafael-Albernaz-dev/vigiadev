from __future__ import annotations

import asyncio
from pathlib import Path

from vigiadev.application.dag_planner import NodeKind, plan_execution
from vigiadev.application.ports import ComposeGateway, HealthGateway, ManifestGateway, PortGateway
from vigiadev.application.supervisor import ProcessRecord, ProcessSupervisor
from vigiadev.domain.errors import HealthcheckError, PortConflictError, ProcessError
from vigiadev.domain.events import (
    EventBus,
    HealthcheckCompleted,
    PhaseChanged,
    PortConflictDetected,
    ServiceStatusChanged,
    ShutdownStarted,
    TaskCompleted,
)
from vigiadev.domain.models import PortPolicy, ServiceConfig, VigiaConfig
from vigiadev.domain.states import GlobalState, ServiceState


class Orchestrator:
    def __init__(
        self,
        config: VigiaConfig,
        project_root: Path,
        event_bus: EventBus,
        supervisor: ProcessSupervisor,
        compose: ComposeGateway | None,
        ports: PortGateway,
        health: HealthGateway,
        manifest: ManifestGateway,
        *,
        kill_ports: bool = False,
    ) -> None:
        self.config = config
        self.project_root = project_root.resolve()
        self.event_bus = event_bus
        self.supervisor = supervisor
        self.compose = compose
        self.ports = ports
        self.health = health
        self.manifest = manifest
        self.kill_ports = kill_ports
        self.state = GlobalState.PLANNING
        self._reused: set[str] = set()
        self._managed_containers: set[str] = set()
        self._stopping = False
        self._stop_requested = False
        self._started = False
        self.supervisor.on_started = self._process_started
        self.supervisor.on_stopped = self._process_stopped

    async def start(self) -> None:
        try:
            await self._phase(GlobalState.PLANNING)
            plan = plan_execution(self.config)
            await self._phase(GlobalState.PREFLIGHT)
            self.manifest.acquire()
            self._started = True
            compose_file = (
                (self.project_root / self.config.compose_file).resolve()
                if self.config.compose_file
                else None
            )
            self.manifest.create(
                self.compose.project_name if self.compose is not None else None,
                compose_file,
            )

            await self._phase(GlobalState.STARTING)
            for node in plan.nodes:
                if self._stop_requested:
                    return
                if node.kind is NodeKind.TASK:
                    await self._run_task(node.name)
                else:
                    await self._start_service(node.name)
            if self._stop_requested:
                return
            await self._phase(GlobalState.RUNNING)
        except asyncio.CancelledError:
            if self._started and self.state is not GlobalState.STOPPED:
                await self.stop("cancelled")
            raise
        except Exception:
            await self._phase(GlobalState.FAILED)
            if self._started:
                await self.stop("startup failure")
            raise

    async def run(self) -> None:
        await self.start()
        if self.supervisor.processes:
            exit_codes = await self.supervisor.wait_until_all_exit()
            if self.state in {GlobalState.STOPPING, GlobalState.STOPPED}:
                return
            failed = {name: code for name, code in exit_codes.items() if code != 0}
            if failed:
                detail = ", ".join(f"{name}={code}" for name, code in failed.items())
                await self.stop("process failure")
                raise ProcessError(f"application processes failed: {detail}")
            await self.stop("all application processes exited")
        else:
            if not self.config.services:
                await self.stop("provisioning tasks completed")
                return
            await asyncio.Future()

    async def stop(self, reason: str = "requested") -> None:
        if self._stopping or self.state is GlobalState.STOPPED:
            return
        self._stop_requested = True
        self._stopping = True
        await self.event_bus.publish(ShutdownStarted(reason=reason))
        await self._phase(GlobalState.STOPPING)
        try:
            await self.supervisor.stop_all()
        finally:
            self.manifest.cleanup()
            await self._phase(GlobalState.STOPPED)
            self._stopping = False

    async def restart_service(self, name: str) -> None:
        service = self.config.services.get(name)
        if service is None:
            raise ProcessError(f"unknown service {name!r}")
        if name in self._reused:
            raise ProcessError(f"refusing to restart reused external service {name!r}")
        await self._service_state(name, ServiceState.STARTING, "restart requested")
        if service.command is not None:
            await self.supervisor.restart(name)
        elif self.compose is not None and service.compose_service is not None:
            await self.compose.restart(service.compose_service)
        else:
            raise ProcessError(f"service {name!r} cannot be restarted")
        await self._wait_for_health(name, service)
        await self._service_state(name, ServiceState.READY)

    async def _start_service(self, name: str) -> None:
        service = self.config.services[name]
        await self._service_state(name, ServiceState.STARTING)
        reused = await self._resolve_ports(name, service)
        if reused:
            self._reused.add(name)
            await self._service_state(name, ServiceState.REUSED, "healthy listener reused")
            return

        if service.compose_service is not None:
            if self.compose is None:
                raise ProcessError("Compose adapter is unavailable")
            containers_before = set(await self.compose.container_names())
            await self.compose.up([service.compose_service])
            containers_after = set(await self.compose.container_names())
            self._managed_containers.update(containers_after - containers_before)
            self.manifest.set_containers(sorted(self._managed_containers))
        elif service.command is not None:
            await self.supervisor.start(
                name,
                service.command,
                self._resolve_cwd(service.cwd),
                service.env,
            )

        await self._wait_for_health(name, service)
        await self._service_state(name, ServiceState.READY)

    async def _resolve_ports(self, name: str, service: ServiceConfig) -> bool:
        occupied = [self.ports.inspect(port) for port in service.ports]
        occupied = [inspection for inspection in occupied if inspection.occupied]
        if not occupied:
            return False

        for inspection in occupied:
            await self.event_bus.publish(
                PortConflictDetected(
                    service=name,
                    port=inspection.port,
                    pid=inspection.pid,
                    policy=service.port_policy.value,
                )
            )

        if service.port_policy is PortPolicy.REUSE:
            if len(occupied) != len(service.ports):
                raise PortConflictError(
                    f"service {name!r} has a partial port conflict; refusing ambiguous reuse"
                )
            if service.healthcheck is not None:
                result = await self.health.wait(
                    service.healthcheck, self._resolve_cwd(service.cwd), service.env
                )
                await self.event_bus.publish(
                    HealthcheckCompleted(
                        service=name,
                        success=result.success,
                        latency_ms=result.latency_ms,
                        detail=result.detail,
                    )
                )
                if not result.success:
                    raise PortConflictError(
                        f"port for {name!r} is occupied but its healthcheck failed: {result.detail}"
                    )
            return True

        if service.port_policy is PortPolicy.KILL:
            if not self.kill_ports:
                raise PortConflictError(
                    f"service {name!r} requires --kill-ports before terminating listeners"
                )
            for inspection in occupied:
                await self.ports.terminate_listener(inspection)
            return False

        if service.port_policy is PortPolicy.PROMPT:
            raise PortConflictError(
                f"service {name!r} requires interactive port confirmation; use fail, reuse, or explicit kill"
            )
        if service.port_policy is PortPolicy.REMAP:
            raise PortConflictError(f"automatic port remapping is not available in MVP 1 for {name!r}")
        raise PortConflictError(f"port already occupied for service {name!r}")

    async def _wait_for_health(self, name: str, service: ServiceConfig) -> None:
        if service.healthcheck is None:
            return
        await self._service_state(name, ServiceState.CHECKING)
        result = await self.health.wait(
            service.healthcheck, self._resolve_cwd(service.cwd), service.env
        )
        await self.event_bus.publish(
            HealthcheckCompleted(
                service=name,
                success=result.success,
                latency_ms=result.latency_ms,
                detail=result.detail,
            )
        )
        if not result.success:
            await self._service_state(name, ServiceState.FAILED, result.detail)
            raise HealthcheckError(f"healthcheck failed for {name!r}: {result.detail}")

    async def _run_task(self, name: str) -> None:
        task = self.config.tasks[name]
        exit_code = await self.supervisor.run_task(
            name,
            task.command,
            self._resolve_cwd(task.cwd),
            task.env,
            task.timeout,
        )
        await self.event_bus.publish(TaskCompleted(task=name, exit_code=exit_code))

    def _resolve_cwd(self, configured: str | None) -> Path:
        if configured is None:
            return self.project_root
        path = Path(configured)
        return path.resolve() if path.is_absolute() else (self.project_root / path).resolve()

    async def _phase(self, state: GlobalState) -> None:
        self.state = state
        await self.event_bus.publish(PhaseChanged(state=state))

    async def _service_state(
        self, name: str, state: ServiceState, detail: str = ""
    ) -> None:
        await self.event_bus.publish(
            ServiceStatusChanged(service=name, state=state, detail=detail)
        )

    def _process_started(self, record: ProcessRecord) -> None:
        self.manifest.add_process(
            record.name, record.pid, record.pgid, record.started_at
        )

    def _process_stopped(self, record: ProcessRecord) -> None:
        self.manifest.remove_process(record.name)
