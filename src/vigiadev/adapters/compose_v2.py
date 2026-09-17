from __future__ import annotations

import asyncio
import json
from collections.abc import Sequence
from pathlib import Path
from typing import Any

from vigiadev.domain.errors import ProcessError


class ComposeV2:
    def __init__(self, project_root: Path, compose_file: Path, project_name: str) -> None:
        self.project_root = project_root.resolve()
        self.compose_file = compose_file.resolve()
        self.project_name = project_name

    async def up(self, services: Sequence[str]) -> None:
        args = ["up", "-d", "--remove-orphans", *services]
        await self._run(*args)

    async def restart(self, service: str) -> None:
        await self._run("restart", service)

    async def down(self) -> None:
        await self._run("down")

    async def container_names(self) -> list[str]:
        output = await self._run("ps", "--format", "json", capture=True)
        if not output.strip():
            return []
        try:
            parsed: Any = json.loads(output)
            rows = parsed if isinstance(parsed, list) else [parsed]
        except json.JSONDecodeError:
            rows = []
            for line in output.splitlines():
                try:
                    rows.append(json.loads(line))
                except json.JSONDecodeError as error:
                    raise ProcessError(f"could not parse Docker Compose ps output: {error}") from error
        names: list[str] = []
        for row in rows:
            if not isinstance(row, dict):
                continue
            name = row.get("Name") or row.get("Names")
            if isinstance(name, str) and name:
                names.append(name)
        return sorted(set(names))

    async def _run(self, *args: str, capture: bool = False) -> str:
        command = [
            "docker",
            "compose",
            "-f",
            str(self.compose_file),
            "--project-name",
            self.project_name,
            *args,
        ]
        try:
            process = await asyncio.create_subprocess_exec(
                *command,
                cwd=self.project_root,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
        except OSError as error:
            raise ProcessError(f"could not execute Docker Compose v2: {error}") from error
        stdout, stderr = await process.communicate()
        output = stdout.decode(errors="replace")
        if process.returncode != 0:
            detail = stderr.decode(errors="replace").strip() or output.strip()
            raise ProcessError(
                f"Docker Compose exited with code {process.returncode}: {detail}"
            )
        if not capture and stderr:
            # Compose progress is informational and never parsed as state.
            output = f"{output}{stderr.decode(errors='replace')}"
        return output

