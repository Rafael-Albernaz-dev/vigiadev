from __future__ import annotations

import json
import sys
from io import StringIO
from pathlib import Path

from rich.console import Console

from vigiadev.cli import main


def test_headless_run_with_real_short_lived_service(tmp_path: Path) -> None:
    config = {
        "version": 1,
        "project_name": "functional",
        "services": {
            "mock-api": {
                "command": [sys.executable, "-c", "print('mock-ready', flush=True)"],
            }
        },
        "tasks": {},
    }
    # JSON is valid YAML and avoids introducing a second serializer in the test.
    (tmp_path / "vigiadev.yaml").write_text(json.dumps(config), encoding="utf-8")
    stream = StringIO()
    console = Console(file=stream, force_terminal=False, color_system=None)
    assert main(["up", "--no-tui"], cwd=tmp_path, console=console) == 0
    output = stream.getvalue()
    assert "mock-ready" in output
    assert "running" in output
    assert "stopped" in output
    assert not (tmp_path / ".vigiadev" / "run.json").exists()
    assert not (tmp_path / ".vigiadev" / "vigiadev.lock").exists()
