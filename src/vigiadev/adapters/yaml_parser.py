from __future__ import annotations

from pathlib import Path
from typing import Any

import yaml
from pydantic import ValidationError

from vigiadev.domain.errors import ConfigurationError
from vigiadev.domain.models import VigiaConfig


CONFIG_CANDIDATES = ("vigiadev.local.yaml", "vigiadev.yaml", "dev.yaml")


def resolve_config_path(project_root: Path, explicit: str | Path | None = None) -> Path:
    root = project_root.resolve()
    if explicit is not None:
        path = Path(explicit)
        path = path if path.is_absolute() else root / path
        if not path.is_file():
            raise ConfigurationError(f"configuration file not found: {path}")
        return path.resolve()

    for filename in CONFIG_CANDIDATES:
        candidate = root / filename
        if candidate.is_file():
            return candidate.resolve()
    searched = ", ".join(CONFIG_CANDIDATES)
    raise ConfigurationError(f"no configuration found in {root}; searched: {searched}")


def load_config(project_root: Path, explicit: str | Path | None = None) -> tuple[Path, VigiaConfig]:
    path = resolve_config_path(project_root, explicit)
    try:
        raw: Any = yaml.safe_load(path.read_text(encoding="utf-8"))
    except (OSError, yaml.YAMLError) as error:
        raise ConfigurationError(f"could not read {path}: {error}") from error
    if not isinstance(raw, dict):
        raise ConfigurationError(f"configuration root must be a mapping: {path}")
    try:
        config = VigiaConfig.model_validate(raw)
    except ValidationError as error:
        raise ConfigurationError(f"invalid configuration {path}:\n{error}") from error
    return path, config

