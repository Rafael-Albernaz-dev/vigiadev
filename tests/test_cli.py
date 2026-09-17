from __future__ import annotations

import os
import signal
import subprocess
import sys
import time
from io import StringIO
from pathlib import Path

from rich.console import Console

from vigiadev.adapters.session_manifest import SessionManifest
from vigiadev.cli import main


def test_init_and_validate(tmp_path: Path) -> None:
    stream = StringIO()
    console = Console(file=stream, force_terminal=False, color_system=None)
    assert main(["init"], cwd=tmp_path, console=console) == 0
    target = tmp_path / "vigiadev.yaml"
    assert target.is_file()
    assert "# vigiaDev configuration" in target.read_text(encoding="utf-8")
    assert main(["validate"], cwd=tmp_path, console=console) == 0
    assert "valid" in stream.getvalue()


def test_init_requires_force_to_overwrite(tmp_path: Path) -> None:
    console = Console(file=StringIO(), force_terminal=False, color_system=None)
    assert main(["init"], cwd=tmp_path, console=console) == 0
    target = tmp_path / "vigiadev.yaml"
    target.write_text("sentinel", encoding="utf-8")
    assert main(["init"], cwd=tmp_path, console=console) == 1
    assert target.read_text(encoding="utf-8") == "sentinel"
    assert main(["init", "--force"], cwd=tmp_path, console=console) == 0
    assert "sentinel" not in target.read_text(encoding="utf-8")


def test_doctor_succeeds_with_declared_host_prerequisites(tmp_path: Path) -> None:
    stream = StringIO()
    console = Console(file=stream, force_terminal=False, color_system=None)
    assert main(["doctor"], cwd=tmp_path, console=console) == 0
    assert "Python >= 3.10" in stream.getvalue()
    assert "Docker Compose v2" in stream.getvalue()


def test_down_stops_only_process_recorded_by_manifest(tmp_path: Path) -> None:
    child = subprocess.Popen(
        [sys.executable, "-c", "import time; time.sleep(30)"],
        preexec_fn=os.setpgrp,
    )
    manifest = SessionManifest(tmp_path)
    manifest.acquire()
    compose_file = tmp_path / "compose.yaml"
    compose_file.write_text("services: {}\n", encoding="utf-8")
    manifest.create("sample", compose_file)
    manifest.add_process("worker", child.pid, os.getpgid(child.pid), time.time())
    console = Console(file=StringIO(), force_terminal=False, color_system=None)
    try:
        # No containers are registered, so explicit down must not touch Compose.
        assert main(["down"], cwd=tmp_path, console=console) == 0
        assert child.wait(timeout=2) == -signal.SIGTERM
        assert not manifest.manifest_path.exists()
        assert not manifest.lock_path.exists()
    finally:
        if child.poll() is None:
            os.killpg(os.getpgid(child.pid), signal.SIGTERM)
            child.wait(timeout=2)

