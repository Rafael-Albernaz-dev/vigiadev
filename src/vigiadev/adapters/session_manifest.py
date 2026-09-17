from __future__ import annotations

import json
import os
import signal
import time
import uuid
from collections.abc import Sequence
from pathlib import Path
from typing import Any

from vigiadev.domain.errors import SessionLockError


class SessionManifest:
    def __init__(self, project_root: Path, run_id: str | None = None) -> None:
        self.project_root = project_root.resolve()
        self.state_dir = self.project_root / ".vigiadev"
        self.manifest_path = self.state_dir / "run.json"
        self.lock_path = self.state_dir / "vigiadev.lock"
        self.run_id = run_id or str(uuid.uuid4())
        self._data: dict[str, Any] = {}
        self._acquired = False

    def acquire(self) -> None:
        self.state_dir.mkdir(parents=True, exist_ok=True)
        lock_data = {
            "run_id": self.run_id,
            "pid": os.getpid(),
            "process_start": _process_start(os.getpid()),
            "created_at": time.time(),
        }
        encoded = (json.dumps(lock_data, sort_keys=True) + "\n").encode()
        try:
            descriptor = os.open(
                self.lock_path,
                os.O_WRONLY | os.O_CREAT | os.O_EXCL,
                0o600,
            )
        except FileExistsError:
            existing = self._read_json(self.lock_path)
            pid = existing.get("pid")
            expected_start = existing.get("process_start")
            if isinstance(pid, int) and _same_process(pid, expected_start):
                raise SessionLockError(
                    f"another vigiaDev session holds {self.lock_path} (PID {pid})"
                )
            self.lock_path.unlink(missing_ok=True)
            return self.acquire()
        with os.fdopen(descriptor, "wb") as lock_file:
            lock_file.write(encoded)
            lock_file.flush()
            os.fsync(lock_file.fileno())
        self._acquired = True

    def create(self, compose_project: str | None, compose_file: Path | None) -> None:
        if not self._acquired:
            raise SessionLockError("session lock must be acquired before creating the manifest")
        self._data = {
            "schema_version": 1,
            "run_id": self.run_id,
            "created_at": time.time(),
            "owner_pid": os.getpid(),
            "owner_process_start": _process_start(os.getpid()),
            "project_root": str(self.project_root),
            "compose_project": compose_project,
            "compose_file": str(compose_file) if compose_file else None,
            "processes": [],
            "containers": [],
        }
        self._write()

    def add_process(
        self, name: str, pid: int, pgid: int, started_at: float
    ) -> None:
        processes = [item for item in self._data.get("processes", []) if item["name"] != name]
        processes.append(
            {
                "name": name,
                "pid": pid,
                "pgid": pgid,
                "started_at": started_at,
                "process_start": _process_start(pid),
            }
        )
        self._data["processes"] = processes
        self._write()

    def remove_process(self, name: str) -> None:
        if not self._data:
            return
        self._data["processes"] = [
            item for item in self._data.get("processes", []) if item["name"] != name
        ]
        self._write()

    def set_containers(self, names: Sequence[str]) -> None:
        self._data["containers"] = sorted(set(names))
        self._write()

    def cleanup(self) -> None:
        manifest = self._read_json(self.manifest_path)
        if manifest.get("run_id") == self.run_id:
            self.manifest_path.unlink(missing_ok=True)
        lock = self._read_json(self.lock_path)
        if lock.get("run_id") == self.run_id:
            self.lock_path.unlink(missing_ok=True)
        self._acquired = False

    def _write(self) -> None:
        if not self._data:
            return
        temporary = self.manifest_path.with_suffix(".json.tmp")
        temporary.write_text(
            json.dumps(self._data, indent=2, sort_keys=True) + "\n", encoding="utf-8"
        )
        os.replace(temporary, self.manifest_path)

    @staticmethod
    def load(project_root: Path) -> dict[str, Any]:
        path = project_root.resolve() / ".vigiadev" / "run.json"
        if not path.is_file():
            raise SessionLockError(f"no active session manifest found: {path}")
        data = SessionManifest._read_json(path)
        if not data:
            raise SessionLockError(f"invalid or empty session manifest: {path}")
        return data

    @staticmethod
    def terminate_recorded_processes(data: dict[str, Any], timeout: float = 5.0) -> None:
        processes = data.get("processes", [])
        owned: list[dict[str, Any]] = []
        for item in processes:
            pid = item.get("pid")
            if isinstance(pid, int) and _same_process(pid, item.get("process_start")):
                owned.append(item)
        for item in owned:
            try:
                os.killpg(int(item["pgid"]), signal.SIGTERM)
            except ProcessLookupError:
                continue
        deadline = time.monotonic() + timeout
        while owned and time.monotonic() < deadline:
            owned = [
                item
                for item in owned
                if _same_process(int(item["pid"]), item.get("process_start"))
            ]
            if owned:
                time.sleep(0.05)
        if owned:
            pids = ", ".join(str(item["pid"]) for item in owned)
            raise SessionLockError(f"owned processes did not stop after SIGTERM: {pids}")

    @staticmethod
    def _read_json(path: Path) -> dict[str, Any]:
        try:
            value = json.loads(path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return {}
        return value if isinstance(value, dict) else {}


def _process_start(pid: int) -> str | None:
    stat = _process_stat(pid)
    return stat[0] if stat is not None else None


def _process_stat(pid: int) -> tuple[str, str] | None:
    try:
        # /proc/<pid>/stat field 22; split after comm because comm may contain spaces.
        suffix = Path(f"/proc/{pid}/stat").read_text(encoding="utf-8").rsplit(")", 1)[1]
        fields = suffix.split()
        return fields[19], fields[0]
    except (OSError, IndexError):
        return None


def _same_process(pid: int, expected_start: object) -> bool:
    stat = _process_stat(pid)
    if stat is None:
        return False
    actual_start, state = stat
    return state != "Z" and actual_start == expected_start
