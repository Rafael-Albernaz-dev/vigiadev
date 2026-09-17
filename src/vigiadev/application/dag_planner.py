from __future__ import annotations

from dataclasses import dataclass
from enum import Enum

from vigiadev.domain.errors import DependencyError
from vigiadev.domain.models import VigiaConfig


class NodeKind(str, Enum):
    SERVICE = "service"
    TASK = "task"


@dataclass(frozen=True, slots=True)
class PlanNode:
    name: str
    kind: NodeKind
    depends_on: tuple[str, ...]


@dataclass(frozen=True, slots=True)
class ExecutionPlan:
    nodes: tuple[PlanNode, ...]

    @property
    def names(self) -> tuple[str, ...]:
        return tuple(node.name for node in self.nodes)


def topological_sort(graph: dict[str, set[str]]) -> list[str]:
    """Return a deterministic dependency-first order or raise with a cycle."""

    all_nodes = set(graph)
    for name, dependencies in graph.items():
        unknown = dependencies - all_nodes
        if unknown:
            raise DependencyError(
                f"node {name!r} depends on unknown nodes: {', '.join(sorted(unknown))}"
            )

    remaining = {name: set(dependencies) for name, dependencies in graph.items()}
    ready = sorted(name for name, dependencies in remaining.items() if not dependencies)
    result: list[str] = []

    while ready:
        current = ready.pop(0)
        result.append(current)
        del remaining[current]
        newly_ready: list[str] = []
        for name, dependencies in remaining.items():
            dependencies.discard(current)
            if not dependencies and name not in ready:
                newly_ready.append(name)
        ready.extend(newly_ready)
        ready.sort()

    if remaining:
        cycle = _find_cycle(remaining)
        cycle_text = " -> ".join(cycle) if cycle else ", ".join(sorted(remaining))
        raise DependencyError(f"dependency cycle detected: {cycle_text}")
    return result


def _find_cycle(graph: dict[str, set[str]]) -> list[str]:
    visited: set[str] = set()
    active: list[str] = []
    active_set: set[str] = set()

    def visit(node: str) -> list[str] | None:
        visited.add(node)
        active.append(node)
        active_set.add(node)
        for dependency in sorted(graph[node]):
            if dependency not in graph:
                continue
            if dependency in active_set:
                index = active.index(dependency)
                return [*active[index:], dependency]
            if dependency not in visited:
                found = visit(dependency)
                if found:
                    return found
        active.pop()
        active_set.remove(node)
        return None

    for node in sorted(graph):
        if node not in visited:
            found = visit(node)
            if found:
                return found
    return []


def plan_execution(config: VigiaConfig) -> ExecutionPlan:
    items = {**config.services, **config.tasks}
    graph = {name: set(item.depends_on) for name, item in items.items()}
    order = topological_sort(graph)
    nodes = tuple(
        PlanNode(
            name=name,
            kind=NodeKind.SERVICE if name in config.services else NodeKind.TASK,
            depends_on=tuple(items[name].depends_on),
        )
        for name in order
    )
    return ExecutionPlan(nodes=nodes)

