from __future__ import annotations

import json
from pathlib import Path

import yaml

from vigiadev.adapters.stack_detector import StackDetector
from vigiadev.domain.models import PortPolicy


def test_detect_compose(tmp_path: Path) -> None:
    (tmp_path / "compose.yaml").write_text(
        """
services:
  postgres:
    image: postgres:16
    ports:
      - "15432:5432"
  redis:
    image: redis:7
    ports:
      - target: 6379
        published: 16379
  worker:
    image: example/worker
""",
        encoding="utf-8",
    )

    config = StackDetector(tmp_path).detect()

    assert config.compose_file == "compose.yaml"
    assert config.services["postgres"].compose_service == "postgres"
    assert config.services["postgres"].ports == [15432]
    assert config.services["redis"].ports == [16379]
    assert config.services["worker"].ports == []
    assert all(
        service.port_policy is PortPolicy.REUSE
        for service in config.services.values()
    )


def test_detect_nodejs_prefers_pnpm_and_vite_port(tmp_path: Path) -> None:
    (tmp_path / "package.json").write_text(
        json.dumps(
            {
                "name": "web-app",
                "scripts": {"dev": "vite", "start": "node server.js"},
                "devDependencies": {"vite": "^7.0.0"},
            }
        ),
        encoding="utf-8",
    )
    (tmp_path / "pnpm-lock.yaml").write_text("lockfileVersion: '9.0'\n", encoding="utf-8")
    (tmp_path / "yarn.lock").write_text("", encoding="utf-8")

    detector = StackDetector(tmp_path)
    config = detector.detect()
    service = config.services["app"]

    assert service.command == ["pnpm", "run", "dev", "--port", "{port}"]
    assert service.ports == [5173]
    assert service.port_policy is PortPolicy.REMAP
    assert service.healthcheck is not None
    assert service.healthcheck.port == 5173
    generated = detector.to_yaml()
    assert "Auto-detected from: Node.js (pnpm, dev)." in generated
    assert yaml.safe_load(generated)["services"]["app"]["ports"] == [5173]


def test_detect_python_django(tmp_path: Path) -> None:
    (tmp_path / "manage.py").write_text("# Django entrypoint\n", encoding="utf-8")

    service = StackDetector(tmp_path).detect().services["app"]

    assert service.command == [
        "python",
        "manage.py",
        "runserver",
        "0.0.0.0:{port}",
    ]
    assert service.ports == [8000]
    assert service.port_policy is PortPolicy.REMAP


def test_detect_python_fastapi(tmp_path: Path) -> None:
    (tmp_path / "pyproject.toml").write_text(
        '[project]\ndependencies = ["fastapi", "uvicorn"]\n',
        encoding="utf-8",
    )
    (tmp_path / "app.py").write_text("from fastapi import FastAPI\n", encoding="utf-8")

    service = StackDetector(tmp_path).detect().services["app"]

    assert service.command == [
        "uvicorn",
        "app:app",
        "--host",
        "0.0.0.0",
        "--port",
        "{port}",
    ]
    assert service.ports == [8000]
