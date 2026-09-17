from __future__ import annotations

from enum import Enum


class GlobalState(str, Enum):
    PLANNING = "planning"
    PREFLIGHT = "preflight"
    STARTING = "starting"
    RUNNING = "running"
    STOPPING = "stopping"
    STOPPED = "stopped"
    FAILED = "failed"


class ServiceState(str, Enum):
    DEFINED = "defined"
    STARTING = "starting"
    CHECKING = "checking"
    READY = "ready"
    REUSED = "reused"
    STOPPING = "stopping"
    STOPPED = "stopped"
    FAILED = "failed"

