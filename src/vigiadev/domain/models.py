from __future__ import annotations

from enum import Enum
from typing import Annotated, Literal

from pydantic import BaseModel, ConfigDict, Field, PositiveFloat, field_validator, model_validator


class StrictModel(BaseModel):
    # YAML strings must be decoded into string-backed enums, while scalar
    # identifiers and ports below opt into strict validation explicitly.
    model_config = ConfigDict(extra="forbid")


PortNumber = Annotated[int, Field(strict=True, ge=1, le=65535)]
StatusCode = Annotated[int, Field(strict=True, ge=100, le=599)]


class PortPolicy(str, Enum):
    REUSE = "reuse"
    PROMPT = "prompt"
    REMAP = "remap"
    FAIL = "fail"
    KILL = "kill"


class HealthType(str, Enum):
    TCP = "tcp"
    HTTP = "http"
    COMMAND = "command"


class HealthCheckConfig(StrictModel):
    type: HealthType
    host: str = "127.0.0.1"
    port: PortNumber | None = None
    url: str | None = None
    command: list[str] | None = None
    expected_status: StatusCode = 200
    timeout: PositiveFloat = 30.0
    interval: PositiveFloat = 0.25

    @field_validator("command")
    @classmethod
    def command_must_not_be_empty(cls, value: list[str] | None) -> list[str] | None:
        if value is not None and (not value or any(not part for part in value)):
            raise ValueError("healthcheck command must contain non-empty arguments")
        return value

    @model_validator(mode="after")
    def validate_strategy_fields(self) -> "HealthCheckConfig":
        if self.type is HealthType.TCP and self.port is None:
            raise ValueError("tcp healthcheck requires port")
        if self.type is HealthType.HTTP and self.url is None:
            raise ValueError("http healthcheck requires url")
        if self.type is HealthType.COMMAND and self.command is None:
            raise ValueError("command healthcheck requires command")
        return self


class RunnableConfig(StrictModel):
    command: list[str]
    depends_on: list[str] = Field(default_factory=list)
    cwd: str | None = None
    env: dict[str, str] = Field(default_factory=dict)

    @field_validator("command")
    @classmethod
    def validate_command(cls, value: list[str]) -> list[str]:
        if not value or any(not part for part in value):
            raise ValueError("command must contain non-empty arguments")
        return value

    @field_validator("depends_on")
    @classmethod
    def dependencies_are_unique(cls, value: list[str]) -> list[str]:
        if len(value) != len(set(value)):
            raise ValueError("depends_on entries must be unique")
        return value


class ServiceConfig(StrictModel):
    command: list[str] | None = None
    compose_service: str | None = None
    depends_on: list[str] = Field(default_factory=list)
    cwd: str | None = None
    env: dict[str, str] = Field(default_factory=dict)
    ports: list[PortNumber] = Field(default_factory=list)
    port_policy: PortPolicy = PortPolicy.REUSE
    healthcheck: HealthCheckConfig | None = None

    @field_validator("command")
    @classmethod
    def validate_command(cls, value: list[str] | None) -> list[str] | None:
        if value is not None and (not value or any(not part for part in value)):
            raise ValueError("command must contain non-empty arguments")
        return value

    @field_validator("ports")
    @classmethod
    def validate_ports(cls, value: list[int]) -> list[int]:
        if any(port < 1 or port > 65535 for port in value):
            raise ValueError("ports must be between 1 and 65535")
        if len(value) != len(set(value)):
            raise ValueError("ports must be unique per service")
        return value

    @field_validator("depends_on")
    @classmethod
    def validate_dependencies(cls, value: list[str]) -> list[str]:
        if len(value) != len(set(value)):
            raise ValueError("depends_on entries must be unique")
        return value

    @model_validator(mode="after")
    def exactly_one_runner(self) -> "ServiceConfig":
        if (self.command is None) == (self.compose_service is None):
            raise ValueError("service requires exactly one of command or compose_service")
        return self


class TaskConfig(RunnableConfig):
    timeout: PositiveFloat = 300.0


class VigiaConfig(StrictModel):
    version: Literal[1] = 1
    project_name: str = Field(min_length=1, pattern=r"^[A-Za-z0-9][A-Za-z0-9_.-]*$")
    compose_file: str | None = None
    services: dict[str, ServiceConfig] = Field(default_factory=dict)
    tasks: dict[str, TaskConfig] = Field(default_factory=dict)

    @model_validator(mode="after")
    def validate_graph_references(self) -> "VigiaConfig":
        overlap = set(self.services) & set(self.tasks)
        if overlap:
            raise ValueError(f"service/task names overlap: {', '.join(sorted(overlap))}")

        nodes = set(self.services) | set(self.tasks)
        missing: dict[str, list[str]] = {}
        for name, item in {**self.services, **self.tasks}.items():
            unknown = sorted(set(item.depends_on) - nodes)
            if unknown:
                missing[name] = unknown
        if missing:
            detail = "; ".join(f"{name}: {deps}" for name, deps in sorted(missing.items()))
            raise ValueError(f"unknown dependencies: {detail}")

        compose_services = [item for item in self.services.values() if item.compose_service]
        if compose_services and self.compose_file is None:
            raise ValueError("compose_file is required when compose services are configured")
        return self

