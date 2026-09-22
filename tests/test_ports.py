from __future__ import annotations

import os
import socket

import pytest

from vigiadev.adapters.port_resolver import PortResolver
from vigiadev.domain.errors import PortConflictError
from vigiadev.domain.models import PortPolicy


def test_detects_free_and_occupied_ports() -> None:
    resolver = PortResolver()
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
        listener.listen()
        occupied = resolver.inspect(port)
        assert occupied.occupied is True
        assert occupied.pid == os.getpid()

    free = resolver.inspect(port)
    assert free.occupied is False
    assert free.pid is None


def test_policy_decisions_are_non_aggressive() -> None:
    resolver = PortResolver()
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
        listener.listen()
        inspection = resolver.inspect(port)
        assert resolver.decision(inspection, PortPolicy.REUSE, healthy=True) == "reuse"
        assert resolver.decision(inspection, PortPolicy.REUSE, healthy=False) == "fail"
        assert resolver.decision(inspection, PortPolicy.FAIL, healthy=True) == "fail"


def _consecutive_listeners(count: int) -> tuple[int, list[socket.socket]]:
    for _ in range(100):
        first = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        first.bind(("127.0.0.1", 0))
        start_port = first.getsockname()[1]
        listeners = [first]
        if start_port + count - 1 > 65535:
            first.close()
            continue
        try:
            for port in range(start_port + 1, start_port + count):
                listener = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
                listener.bind(("127.0.0.1", port))
                listeners.append(listener)
            for listener in listeners:
                listener.listen()
            return start_port, listeners
        except OSError:
            for listener in listeners:
                listener.close()
    raise AssertionError("could not reserve consecutive local ports for test")


def test_find_available_port_scans_sequentially() -> None:
    start_port, listeners = _consecutive_listeners(2)
    try:
        assert PortResolver().find_available_port(start_port) == start_port + 2
    finally:
        for listener in listeners:
            listener.close()


def test_find_available_port_has_deterministic_attempt_limit() -> None:
    start_port, listeners = _consecutive_listeners(2)
    try:
        with pytest.raises(PortConflictError, match="after 1 attempts"):
            PortResolver().find_available_port(start_port, max_attempts=1)
    finally:
        for listener in listeners:
            listener.close()

