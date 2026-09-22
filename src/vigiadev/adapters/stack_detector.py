from __future__ import annotations

import json
import re
from pathlib import Path
from typing import Any

import yaml

from vigiadev.adapters.yaml_parser import dump_config
from vigiadev.domain.errors import ConfigurationError
from vigiadev.domain.models import (
    HealthCheckConfig,
    HealthType,
    PortPolicy,
    ServiceConfig,
    VigiaConfig,
)


class StackDetector:
    """Inspect known project markers without executing project code."""

    COMPOSE_FILES = ("compose.yaml", "docker-compose.yml")

    def __init__(self, root: Path) -> None:
        self.root = root.resolve()
        self._detected: VigiaConfig | None = None
        self._markers: list[str] = []

    def detect(self) -> VigiaConfig:
        services: dict[str, ServiceConfig] = {}
        self._markers = []
        compose_file = self._detect_compose(services)
        self._detect_node(services)
        self._detect_python(services)
        self._detected = VigiaConfig(
            project_name=self._project_name(),
            compose_file=compose_file,
            services=services,
        )
        return self._detected

    def to_yaml(self) -> str:
        config = self._detected or self.detect()
        detected = ", ".join(self._markers) if self._markers else "no known stack markers"
        return dump_config(
            config,
            comments=[
                f"Auto-detected from: {detected}.",
                "Review commands, ports, and healthchecks before sharing this file.",
                "{port} is replaced only for services using port_policy: remap.",
            ],
        )

    def _detect_compose(self, services: dict[str, ServiceConfig]) -> str | None:
        compose_path = next(
            (
                self.root / name
                for name in self.COMPOSE_FILES
                if (self.root / name).is_file()
            ),
            None,
        )
        if compose_path is None:
            return None
        try:
            raw: Any = yaml.safe_load(compose_path.read_text(encoding="utf-8"))
        except (OSError, yaml.YAMLError) as error:
            raise ConfigurationError(f"could not inspect {compose_path}: {error}") from error
        compose_services = raw.get("services") if isinstance(raw, dict) else None
        if not isinstance(compose_services, dict):
            raise ConfigurationError(f"Compose file has no services mapping: {compose_path}")
        for raw_name, definition in compose_services.items():
            name = self._unique_service_name(str(raw_name), services)
            details = definition if isinstance(definition, dict) else {}
            services[name] = ServiceConfig(
                compose_service=str(raw_name),
                ports=self._compose_ports(details.get("ports", [])),
                port_policy=PortPolicy.REUSE,
            )
        self._markers.append(compose_path.name)
        return compose_path.name

    def _detect_node(self, services: dict[str, ServiceConfig]) -> None:
        package_path = self.root / "package.json"
        if not package_path.is_file():
            return
        try:
            package = json.loads(package_path.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError) as error:
            raise ConfigurationError(f"could not inspect {package_path}: {error}") from error
        scripts = package.get("scripts", {}) if isinstance(package, dict) else {}
        script = next(
            (
                candidate
                for candidate in ("dev", "start")
                if isinstance(scripts, dict) and isinstance(scripts.get(candidate), str)
            ),
            None,
        )
        if script is None:
            return

        manager = self._node_manager()
        script_body = str(scripts[script]).lower()
        dependencies = {
            **(
                package.get("dependencies", {})
                if isinstance(package.get("dependencies"), dict)
                else {}
            ),
            **(
                package.get("devDependencies", {})
                if isinstance(package.get("devDependencies"), dict)
                else {}
            ),
        }
        dependency_names = {str(name).lower() for name in dependencies}
        port = 5173 if "vite" in script_body or "vite" in dependency_names else 3000
        name = self._unique_service_name("app", services)
        services[name] = ServiceConfig(
            command=self._node_command(manager, script),
            ports=[port],
            port_policy=PortPolicy.REMAP,
            healthcheck=HealthCheckConfig(type=HealthType.TCP, port=port),
        )
        self._markers.append(f"Node.js ({manager}, {script})")

    def _detect_python(self, services: dict[str, ServiceConfig]) -> None:
        manage_py = self.root / "manage.py"
        if manage_py.is_file():
            name = self._unique_service_name("app", services)
            services[name] = ServiceConfig(
                command=["python", "manage.py", "runserver", "0.0.0.0:{port}"],
                ports=[8000],
                port_policy=PortPolicy.REMAP,
                healthcheck=HealthCheckConfig(type=HealthType.TCP, port=8000),
            )
            self._markers.append("Python (Django)")
            return

        dependency_files = [
            path
            for path in (self.root / "pyproject.toml", self.root / "requirements.txt")
            if path.is_file()
        ]
        contents: list[str] = []
        for path in dependency_files:
            try:
                contents.append(path.read_text(encoding="utf-8").lower())
            except OSError as error:
                raise ConfigurationError(f"could not inspect {path}: {error}") from error
        dependency_text = "\n".join(contents)
        if "fastapi" not in dependency_text and "uvicorn" not in dependency_text:
            return
        module = (
            "app"
            if (self.root / "app.py").is_file()
            and not (self.root / "main.py").is_file()
            else "main"
        )
        name = self._unique_service_name("app", services)
        services[name] = ServiceConfig(
            command=[
                "uvicorn",
                f"{module}:app",
                "--host",
                "0.0.0.0",
                "--port",
                "{port}",
            ],
            ports=[8000],
            port_policy=PortPolicy.REMAP,
            healthcheck=HealthCheckConfig(type=HealthType.TCP, port=8000),
        )
        self._markers.append("Python (FastAPI/Uvicorn)")

    def _node_manager(self) -> str:
        for manager, markers in (
            ("pnpm", ("pnpm-lock.yaml",)),
            ("yarn", ("yarn.lock",)),
            ("bun", ("bun.lock", "bun.lockb")),
        ):
            if any((self.root / marker).is_file() for marker in markers):
                return manager
        return "npm"

    @staticmethod
    def _node_command(manager: str, script: str) -> list[str]:
        if manager == "npm":
            return ["npm", "run", script, "--", "--port", "{port}"]
        if manager == "yarn":
            return ["yarn", script, "--port", "{port}"]
        return [manager, "run", script, "--port", "{port}"]

    @staticmethod
    def _compose_ports(raw_ports: Any) -> list[int]:
        if not isinstance(raw_ports, list):
            return []
        ports: list[int] = []
        for entry in raw_ports:
            candidate: Any = entry
            if isinstance(entry, dict):
                candidate = entry.get("published", entry.get("target"))
            elif isinstance(entry, str):
                without_protocol = entry.rsplit("/", 1)[0]
                candidate = (
                    without_protocol.split(":")[-2]
                    if ":" in without_protocol
                    else without_protocol
                )
                candidate = str(candidate).split("-", 1)[0]
            try:
                port = int(candidate)
            except (TypeError, ValueError):
                continue
            if 1 <= port <= 65535 and port not in ports:
                ports.append(port)
        return ports

    def _project_name(self) -> str:
        normalized = re.sub(r"[^A-Za-z0-9_.-]+", "-", self.root.name).strip("._-")
        return normalized or "project"

    @staticmethod
    def _unique_service_name(
        preferred: str, services: dict[str, ServiceConfig]
    ) -> str:
        if preferred not in services:
            return preferred
        suffix = 2
        while f"{preferred}-{suffix}" in services:
            suffix += 1
        return f"{preferred}-{suffix}"
