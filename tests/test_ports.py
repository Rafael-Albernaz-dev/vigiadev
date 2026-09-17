from __future__ import annotations

import os
import socket

from vigiadev.adapters.port_resolver import PortResolver
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

