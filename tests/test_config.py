from __future__ import annotations

from pathlib import Path

import pytest

from vigiadev.adapters.yaml_parser import load_config, resolve_config_path
from vigiadev.domain.errors import ConfigurationError


VALID = """version: 1
project_name: sample
services:
  api:
    command: [\"python3\", \"-m\", \"http.server\"]
    ports: [8000]
tasks: {}
"""


def test_resolution_precedence(tmp_path: Path) -> None:
    legacy = tmp_path / "dev.yaml"
    default = tmp_path / "vigiadev.yaml"
    local = tmp_path / "vigiadev.local.yaml"
    explicit = tmp_path / "custom.yaml"
    for path in (legacy, default, local, explicit):
        path.write_text(VALID, encoding="utf-8")

    assert resolve_config_path(tmp_path) == local
    local.unlink()
    assert resolve_config_path(tmp_path) == default
    default.unlink()
    assert resolve_config_path(tmp_path) == legacy
    assert resolve_config_path(tmp_path, "custom.yaml") == explicit


def test_schema_loads_valid_configuration(tmp_path: Path) -> None:
    path = tmp_path / "vigiadev.yaml"
    path.write_text(VALID, encoding="utf-8")
    resolved, config = load_config(tmp_path)
    assert resolved == path
    assert config.project_name == "sample"
    assert config.services["api"].ports == [8000]


@pytest.mark.parametrize(
    "invalid",
    [
        "version: 1\nproject_name: sample\nservices:\n  api:\n    command: []\n",
        "version: 1\nproject_name: sample\nservices:\n  api:\n    command: [python3]\n    ports: [\"8000\"]\n",
        "version: 1\nproject_name: sample\nservices:\n  api:\n    command: [python3]\n    depends_on: [missing]\n",
        "version: 1\nproject_name: sample\nunknown: true\n",
    ],
)
def test_schema_rejects_invalid_configuration(tmp_path: Path, invalid: str) -> None:
    (tmp_path / "vigiadev.yaml").write_text(invalid, encoding="utf-8")
    with pytest.raises(ConfigurationError):
        load_config(tmp_path)


def test_missing_configuration_is_clear(tmp_path: Path) -> None:
    with pytest.raises(ConfigurationError, match="no configuration found"):
        resolve_config_path(tmp_path)

