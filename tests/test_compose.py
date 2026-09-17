from __future__ import annotations

from pathlib import Path

import pytest

from vigiadev.adapters.compose_v2 import ComposeV2


@pytest.mark.asyncio
async def test_compose_up_is_non_destructive_and_tracks_containers(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    binary_dir = tmp_path / "bin"
    binary_dir.mkdir()
    command_log = tmp_path / "docker-commands.log"
    docker = binary_dir / "docker"
    docker.write_text(
        "#!/bin/sh\n"
        'printf "%s\\n" "$*" >> "$VIGIADEV_TEST_DOCKER_LOG"\n'
        'case "$*" in\n'
        '  *" ps --format json") printf \'[{"Name":"sample-db-1"}]\\n\' ;;\n'
        "esac\n",
        encoding="utf-8",
    )
    docker.chmod(0o755)
    monkeypatch.setenv("PATH", f"{binary_dir}")
    monkeypatch.setenv("VIGIADEV_TEST_DOCKER_LOG", str(command_log))

    compose_file = tmp_path / "compose.yaml"
    compose_file.write_text("services: {}\n", encoding="utf-8")
    adapter = ComposeV2(tmp_path, compose_file, "sample")

    await adapter.up(["database"])
    assert await adapter.container_names() == ["sample-db-1"]

    commands = command_log.read_text(encoding="utf-8").splitlines()
    assert commands[0].endswith(
        "--project-name sample up -d --remove-orphans database"
    )
    assert all(" down" not in command for command in commands)

    await adapter.down()
    assert command_log.read_text(encoding="utf-8").splitlines()[-1].endswith(
        "--project-name sample down"
    )
