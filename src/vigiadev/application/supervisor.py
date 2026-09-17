from __future__ import annotations

import asyncio
import os
import signal
import time
from collections.abc import Awaitable, Callable, Sequence
from dataclasses import dataclass
from pathlib import Path

from vigiadev.domain.errors import ProcessError
from vigiadev.domain.events import EventBus, LogReceived, ProcessExited


@dataclass(slots=True)
class ProcessRecord:
    name: str
    command: tuple[str, ...]
    cwd: Path
    env: dict[str, str]
    process: asyncio.subprocess.Process
    pid: int
    pgid: int
    started_at: float
    stdout_task: asyncio.Task[None] | None = None
    stderr_task: asyncio.Task[None] | None = None


ProcessStartedHook = Callable[[ProcessRecord], Awaitable[None] | None]
ProcessStoppedHook = Callable[[ProcessRecord], Awaitable[None] | None]


class ProcessSupervisor:
    """The sole owner of spawned application process groups."""

    def __init__(self, event_bus: EventBus) -> None:
        self._event_bus = event_bus
        self._processes: dict[str, ProcessRecord] = {}
        self._specs: dict[str, tuple[tuple[str, ...], Path, dict[str, str]]] = {}
        self.on_started: ProcessStartedHook | None = None
        self.on_stopped: ProcessStoppedHook | None = None

    @property
    def processes(self) -> dict[str, ProcessRecord]:
        return dict(self._processes)

    async def start(
        self,
        name: str,
        command: Sequence[str],
        cwd: Path,
        env: dict[str, str] | None = None,
    ) -> ProcessRecord:
        if name in self._processes and self._processes[name].process.returncode is None:
            raise ProcessError(f"process {name!r} is already running")
        if not command:
            raise ProcessError(f"process {name!r} has an empty command")

        merged_env = os.environ.copy()
        merged_env.update(env or {})
        try:
            process = await asyncio.create_subprocess_exec(
                *command,
                cwd=cwd,
                env=merged_env,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                preexec_fn=os.setpgrp,
            )
        except (OSError, ValueError) as error:
            raise ProcessError(f"could not start {name!r}: {error}") from error

        started_at = time.time()
        record = ProcessRecord(
            name=name,
            command=tuple(command),
            cwd=cwd,
            env=dict(env or {}),
            process=process,
            pid=process.pid,
            pgid=os.getpgid(process.pid),
            started_at=started_at,
        )
        record.stdout_task = asyncio.create_task(
            self._pump_stream(record, process.stdout, "stdout")
        )
        record.stderr_task = asyncio.create_task(
            self._pump_stream(record, process.stderr, "stderr")
        )
        self._processes[name] = record
        self._specs[name] = (tuple(command), cwd, dict(env or {}))
        await self._call_hook(self.on_started, record)
        return record

    async def run_task(
        self,
        name: str,
        command: Sequence[str],
        cwd: Path,
        env: dict[str, str] | None = None,
        timeout: float = 300.0,
    ) -> int:
        record = await self.start(name, command, cwd, env)
        try:
            exit_code = await asyncio.wait_for(record.process.wait(), timeout=timeout)
        except TimeoutError as error:
            await self.stop(name)
            raise ProcessError(f"task {name!r} timed out after {timeout:g}s") from error
        await self._finalize(record)
        if exit_code != 0:
            raise ProcessError(f"task {name!r} exited with code {exit_code}")
        return exit_code

    async def wait(self, name: str) -> int:
        record = self._processes.get(name)
        if record is None:
            raise ProcessError(f"unknown process {name!r}")
        exit_code = await record.process.wait()
        await self._finalize(record)
        return exit_code

    async def wait_until_all_exit(self) -> dict[str, int]:
        records = list(self._processes.values())
        if not records:
            return {}
        results = await asyncio.gather(
            *(record.process.wait() for record in records), return_exceptions=False
        )
        exit_codes: dict[str, int] = {}
        for record, exit_code in zip(records, results, strict=True):
            exit_codes[record.name] = exit_code
            await self._finalize(record)
        return exit_codes

    async def stop(self, name: str, timeout: float = 5.0) -> None:
        record = self._processes.get(name)
        if record is None:
            return
        if record.process.returncode is None:
            try:
                os.killpg(record.pgid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            try:
                await asyncio.wait_for(record.process.wait(), timeout=timeout)
            except TimeoutError:
                # SIGKILL is restricted to a process group created and recorded by us.
                try:
                    os.killpg(record.pgid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                await record.process.wait()
        await self._finalize(record)

    async def stop_all(self, timeout: float = 5.0) -> None:
        for name in reversed(list(self._processes)):
            await self.stop(name, timeout=timeout)

    async def restart(self, name: str) -> ProcessRecord:
        if name not in self._specs:
            raise ProcessError(f"no owned process specification for {name!r}")
        command, cwd, env = self._specs[name]
        await self.stop(name)
        return await self.start(name, command, cwd, env)

    async def _pump_stream(
        self,
        record: ProcessRecord,
        stream: asyncio.StreamReader | None,
        stream_name: str,
    ) -> None:
        if stream is None:
            return
        while line := await stream.readline():
            await self._event_bus.publish(
                LogReceived(
                    service=record.name,
                    stream=stream_name,
                    message=line.decode(errors="replace").rstrip("\n"),
                )
            )

    async def _finalize(self, record: ProcessRecord) -> None:
        current = self._processes.get(record.name)
        if current is not record:
            return
        for task in (record.stdout_task, record.stderr_task):
            if task is not None:
                await task
        self._processes.pop(record.name, None)
        await self._call_hook(self.on_stopped, record)
        await self._event_bus.publish(
            ProcessExited(service=record.name, exit_code=record.process.returncode or 0)
        )

    @staticmethod
    async def _call_hook(
        hook: ProcessStartedHook | ProcessStoppedHook | None, record: ProcessRecord
    ) -> None:
        if hook is None:
            return
        result = hook(record)
        if asyncio.iscoroutine(result):
            await result

