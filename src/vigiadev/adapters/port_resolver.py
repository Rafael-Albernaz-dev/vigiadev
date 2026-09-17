from __future__ import annotations

import asyncio
import os
import signal
import socket
from pathlib import Path

from vigiadev.application.ports import PortInspection
from vigiadev.domain.errors import PortConflictError
from vigiadev.domain.models import PortPolicy


class PortResolver:
    def inspect(self, port: int, host: str = "127.0.0.1") -> PortInspection:
        family = socket.AF_INET6 if ":" in host else socket.AF_INET
        with socket.socket(family, socket.SOCK_STREAM) as probe:
            probe.settimeout(0.2)
            occupied = probe.connect_ex((host, port)) == 0
        return PortInspection(
            port=port,
            occupied=occupied,
            pid=self._find_listener_pid(port) if occupied else None,
        )

    def decision(
        self, inspection: PortInspection, policy: PortPolicy, healthy: bool
    ) -> str:
        if not inspection.occupied:
            return "available"
        if policy is PortPolicy.REUSE and healthy:
            return "reuse"
        if policy is PortPolicy.KILL:
            return "kill"
        if policy is PortPolicy.REMAP:
            return "remap"
        return "fail"

    async def terminate_listener(
        self, inspection: PortInspection, timeout: float = 5.0
    ) -> None:
        if not inspection.occupied:
            return
        if inspection.pid is None:
            raise PortConflictError(
                f"port {inspection.port} is occupied but its listener PID is unknown"
            )
        if inspection.pid == os.getpid():
            raise PortConflictError(
                f"refusing to terminate current vigiaDev process on port {inspection.port}"
            )
        try:
            os.kill(inspection.pid, signal.SIGTERM)
        except (ProcessLookupError, PermissionError) as error:
            raise PortConflictError(
                f"could not terminate PID {inspection.pid} on port {inspection.port}: {error}"
            ) from error

        deadline = asyncio.get_running_loop().time() + timeout
        while asyncio.get_running_loop().time() < deadline:
            if not self.inspect(inspection.port).occupied:
                return
            await asyncio.sleep(0.05)
        raise PortConflictError(
            f"PID {inspection.pid} did not release port {inspection.port} after SIGTERM"
        )

    @staticmethod
    def _find_listener_pid(port: int) -> int | None:
        inodes: set[str] = set()
        encoded_port = f"{port:04X}"
        for table in (Path("/proc/net/tcp"), Path("/proc/net/tcp6")):
            try:
                lines = table.read_text(encoding="utf-8").splitlines()[1:]
            except OSError:
                continue
            for line in lines:
                fields = line.split()
                if len(fields) < 10:
                    continue
                local_address = fields[1]
                state = fields[3]
                if local_address.rsplit(":", 1)[-1] == encoded_port and state == "0A":
                    inodes.add(fields[9])
        if not inodes:
            return None

        proc = Path("/proc")
        for pid_path in proc.iterdir():
            if not pid_path.name.isdigit():
                continue
            fd_path = pid_path / "fd"
            try:
                descriptors = list(fd_path.iterdir())
            except OSError:
                continue
            for descriptor in descriptors:
                try:
                    target = os.readlink(descriptor)
                except OSError:
                    continue
                if target.startswith("socket:[") and target[8:-1] in inodes:
                    return int(pid_path.name)
        return None

