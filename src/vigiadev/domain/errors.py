"""Domain-level errors with no infrastructure dependencies."""


class VigiaDevError(Exception):
    """Base error rendered as a concise CLI failure."""


class ConfigurationError(VigiaDevError):
    """Configuration could not be resolved or validated."""


class DependencyError(VigiaDevError):
    """Dependency graph is missing a node or contains a cycle."""


class PortConflictError(VigiaDevError):
    """A port conflict could not be resolved safely."""


class HealthcheckError(VigiaDevError):
    """A semantic readiness check did not succeed in time."""


class ProcessError(VigiaDevError):
    """An owned child process failed."""


class SessionLockError(VigiaDevError):
    """Another vigiaDev session already owns the project lock."""

