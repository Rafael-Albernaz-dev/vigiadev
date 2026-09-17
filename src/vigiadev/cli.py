from __future__ import annotations

import argparse
import asyncio
import contextlib
import os
import shutil
import signal
import subprocess
import sys
from pathlib import Path
from typing import Sequence

from rich.console import Console

from vigiadev.adapters.compose_v2 import ComposeV2
from vigiadev.adapters.health import HealthChecker
from vigiadev.adapters.port_resolver import PortResolver
from vigiadev.adapters.session_manifest import SessionManifest
from vigiadev.adapters.yaml_parser import load_config
from vigiadev.application.orchestrator import Orchestrator
from vigiadev.application.supervisor import ProcessSupervisor
from vigiadev.domain.errors import VigiaDevError
from vigiadev.domain.events import EventBus
from vigiadev.presentation.stream_view import StreamView
from vigiadev.presentation.textual_app import VigiaDevApp


CANONICAL_TEMPLATE = """# vigiaDev configuration (version 1)
version: 1
project_name: my-project

# Set compose_file when at least one service uses compose_service.
# compose_file: docker-compose.yml

services:
  app:
    command: [\"python\", \"-m\", \"my_app\"]
    ports: [8000]
    port_policy: reuse  # reuse | prompt | remap | fail | kill
    healthcheck:
      type: http
      url: http://127.0.0.1:8000/health
      expected_status: 200
      timeout: 30
      interval: 0.25

# Provisioning tasks are DAG nodes and run once after their dependencies.
tasks: {}
"""


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="vigiadev")
    subparsers = parser.add_subparsers(dest="command", required=True)

    init_parser = subparsers.add_parser("init", help="create a canonical vigiadev.yaml")
    init_parser.add_argument("--force", action="store_true", help="overwrite an existing file")

    up_parser = subparsers.add_parser("up", help="start the development environment")
    up_parser.add_argument("--no-tui", action="store_true", help="stream events to stdout")
    up_parser.add_argument("--config", "-c", metavar="PATH")
    up_parser.add_argument(
        "--kill-ports",
        action="store_true",
        help="allow SIGTERM only for services declaring port_policy: kill",
    )

    down_parser = subparsers.add_parser("down", help="stop resources in the active manifest")
    down_parser.add_argument("--keep-infra", action="store_true", help="do not run Compose down")

    validate_parser = subparsers.add_parser("validate", help="validate configuration")
    validate_parser.add_argument("--config", "-c", metavar="PATH")

    subparsers.add_parser("doctor", help="check local prerequisites")
    return parser


def main(
    argv: Sequence[str] | None = None,
    *,
    cwd: Path | None = None,
    console: Console | None = None,
) -> int:
    args = build_parser().parse_args(argv)
    root = (cwd or Path.cwd()).resolve()
    output = console or Console()
    try:
        if args.command == "init":
            return _init(root, args.force, output)
        if args.command == "validate":
            path, config = load_config(root, args.config)
            output.print(
                f"[green]valid[/] {path} ({len(config.services)} services, {len(config.tasks)} tasks)"
            )
            return 0
        if args.command == "doctor":
            return _doctor(output)
        if args.command == "down":
            return asyncio.run(_down(root, args.keep_infra, output))
        if args.command == "up":
            return asyncio.run(_up(root, args.config, args.no_tui, args.kill_ports, output))
    except (VigiaDevError, OSError) as error:
        output.print(f"[red]error:[/] {error}", highlight=False)
        return 1
    return 2


def entrypoint() -> None:
    raise SystemExit(main())


def _init(root: Path, force: bool, console: Console) -> int:
    target = root / "vigiadev.yaml"
    if target.exists() and not force:
        raise VigiaDevError(f"{target} already exists; pass --force to overwrite it")
    target.write_text(CANONICAL_TEMPLATE, encoding="utf-8")
    console.print(f"[green]created[/] {target}")
    return 0


def _doctor(console: Console) -> int:
    checks: list[tuple[str, bool, str]] = []
    python_ok = sys.version_info >= (3, 10)
    checks.append(("Python >= 3.10", python_ok, sys.version.split()[0]))

    docker_path = shutil.which("docker")
    if docker_path is None:
        checks.append(("Docker CLI", False, "not found on PATH"))
        checks.append(("Docker Compose v2", False, "Docker CLI unavailable"))
    else:
        checks.append(("Docker CLI", True, docker_path))
        try:
            result = subprocess.run(
                [docker_path, "compose", "version"],
                capture_output=True,
                text=True,
                timeout=5,
                check=False,
            )
            detail = (result.stdout or result.stderr).strip()
            checks.append(("Docker Compose v2", result.returncode == 0, detail))
        except (OSError, subprocess.TimeoutExpired) as error:
            checks.append(("Docker Compose v2", False, str(error)))

    for name, success, detail in checks:
        console.print(f"[{'green' if success else 'red'}]{'OK' if success else 'FAIL'}[/] {name}: {detail}")
    return 0 if all(success for _, success, _ in checks) else 1


async def _up(
    root: Path,
    explicit_config: str | None,
    no_tui: bool,
    kill_ports: bool,
    console: Console,
) -> int:
    _, config = load_config(root, explicit_config)
    bus = EventBus()
    supervisor = ProcessSupervisor(bus)
    manifest = SessionManifest(root)
    compose = None
    if config.compose_file is not None:
        compose = ComposeV2(root, root / config.compose_file, config.project_name)
    orchestrator = Orchestrator(
        config,
        root,
        bus,
        supervisor,
        compose,
        PortResolver(),
        HealthChecker(),
        manifest,
        kill_ports=kill_ports,
    )

    if no_tui:
        view = StreamView(bus, console)
        try:
            await _run_headless(orchestrator)
        finally:
            view.close()
        return 0

    app = VigiaDevApp(
        bus,
        {name: service.ports for name, service in config.services.items()},
        lambda: orchestrator.stop("TUI quit"),
        orchestrator.restart_service,
    )
    await _run_tui(app, orchestrator)
    return 0


async def _run_headless(orchestrator: Orchestrator) -> None:
    stop_requested = asyncio.Event()
    loop = asyncio.get_running_loop()
    installed_signals: list[signal.Signals] = []
    for signum in (signal.SIGINT, signal.SIGTERM):
        with contextlib.suppress(NotImplementedError):
            loop.add_signal_handler(signum, stop_requested.set)
            installed_signals.append(signum)

    run_task = asyncio.create_task(orchestrator.run())
    signal_task = asyncio.create_task(stop_requested.wait())
    try:
        done, _ = await asyncio.wait(
            {run_task, signal_task}, return_when=asyncio.FIRST_COMPLETED
        )
        if signal_task in done:
            await orchestrator.stop("signal")
            run_task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await run_task
        else:
            signal_task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await signal_task
            await run_task
    finally:
        for signum in installed_signals:
            loop.remove_signal_handler(signum)


async def _run_tui(app: VigiaDevApp, orchestrator: Orchestrator) -> None:
    app_task = asyncio.create_task(app.run_async())
    ready_task = asyncio.create_task(app.ready_event.wait())
    done, _ = await asyncio.wait(
        {app_task, ready_task}, return_when=asyncio.FIRST_COMPLETED
    )
    if app_task in done:
        ready_task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await ready_task
        await app_task
        return

    await ready_task
    run_task = asyncio.create_task(orchestrator.run())
    done, _ = await asyncio.wait({app_task, run_task}, return_when=asyncio.FIRST_COMPLETED)
    if run_task in done:
        try:
            await run_task
        finally:
            app.exit()
            await app_task
    else:
        await app_task
        await orchestrator.stop("TUI closed")
        run_task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await run_task


async def _down(root: Path, keep_infra: bool, console: Console) -> int:
    data = SessionManifest.load(root)
    recorded_root = Path(str(data.get("project_root", ""))).resolve()
    if recorded_root != root:
        raise VigiaDevError(
            f"manifest belongs to {recorded_root}, not current project {root}"
        )
    SessionManifest.terminate_recorded_processes(data)

    compose_project = data.get("compose_project")
    compose_file = data.get("compose_file")
    containers = data.get("containers")
    owns_containers = isinstance(containers, list) and bool(containers)
    if (
        not keep_infra
        and owns_containers
        and isinstance(compose_project, str)
        and isinstance(compose_file, str)
    ):
        await ComposeV2(root, Path(compose_file), compose_project).down()

    session = SessionManifest(root, run_id=str(data["run_id"]))
    session.cleanup()
    console.print("[green]stopped[/] resources recorded by the active vigiaDev session")
    return 0
