from __future__ import annotations

import json
import os
import time
from pathlib import Path

import pytest

from vigiadev.adapters.session_manifest import SessionManifest
from vigiadev.domain.errors import SessionLockError


def test_manifest_and_lock_lifecycle(tmp_path: Path) -> None:
    manifest = SessionManifest(tmp_path, run_id="run-one")
    manifest.acquire()
    manifest.create("sample", tmp_path / "compose.yaml")
    manifest.add_process("api", os.getpid(), os.getpgrp(), time.time())
    manifest.set_containers(["sample-db-1", "sample-api-1", "sample-db-1"])

    data = json.loads(manifest.manifest_path.read_text(encoding="utf-8"))
    assert data["run_id"] == "run-one"
    assert data["processes"][0]["pid"] == os.getpid()
    assert data["containers"] == ["sample-api-1", "sample-db-1"]
    assert manifest.lock_path.is_file()

    competing = SessionManifest(tmp_path, run_id="run-two")
    with pytest.raises(SessionLockError, match="another vigiaDev session"):
        competing.acquire()

    manifest.cleanup()
    assert not manifest.manifest_path.exists()
    assert not manifest.lock_path.exists()


def test_stale_lock_is_recovered(tmp_path: Path) -> None:
    state_dir = tmp_path / ".vigiadev"
    state_dir.mkdir()
    (state_dir / "vigiadev.lock").write_text(
        '{"run_id":"stale","pid":99999999,"process_start":"0"}', encoding="utf-8"
    )
    manifest = SessionManifest(tmp_path)
    manifest.acquire()
    manifest.create(None, None)
    assert SessionManifest.load(tmp_path)["run_id"] == manifest.run_id
    manifest.cleanup()

