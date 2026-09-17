from __future__ import annotations

import pytest

from vigiadev.application.dag_planner import plan_execution, topological_sort
from vigiadev.domain.errors import DependencyError
from vigiadev.domain.models import ServiceConfig, TaskConfig, VigiaConfig


def test_topological_order_is_dependency_first_and_deterministic() -> None:
    graph = {
        "frontend": {"api"},
        "api": {"migrate", "cache"},
        "migrate": {"database"},
        "cache": set(),
        "database": set(),
    }
    order = topological_sort(graph)
    positions = {name: index for index, name in enumerate(order)}
    for name, dependencies in graph.items():
        assert all(positions[dependency] < positions[name] for dependency in dependencies)
    assert order == topological_sort(graph)


def test_cycle_reports_the_cycle() -> None:
    with pytest.raises(DependencyError, match=r"cycle.*a.*b.*c.*a"):
        topological_sort({"a": {"b"}, "b": {"c"}, "c": {"a"}})


def test_execution_plan_combines_tasks_and_services() -> None:
    config = VigiaConfig(
        project_name="sample",
        services={
            "database": ServiceConfig(compose_service="db"),
            "api": ServiceConfig(command=["python3", "-m", "api"], depends_on=["migrate"]),
        },
        tasks={
            "migrate": TaskConfig(command=["python3", "migrate.py"], depends_on=["database"])
        },
        compose_file="compose.yaml",
    )
    assert plan_execution(config).names == ("database", "migrate", "api")

