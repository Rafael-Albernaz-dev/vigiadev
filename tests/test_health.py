from __future__ import annotations

import asyncio
import contextlib
import http.server
import socket
import sys
import threading
from pathlib import Path

import pytest

from vigiadev.adapters.health import HealthChecker
from vigiadev.domain.models import HealthCheckConfig, HealthType


@pytest.mark.asyncio
async def test_tcp_healthcheck(tmp_path: Path) -> None:
    server = await asyncio.start_server(lambda reader, writer: writer.close(), "127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    try:
        result = await HealthChecker().wait(
            HealthCheckConfig(type=HealthType.TCP, port=port, timeout=1.0, interval=0.01),
            tmp_path,
            {},
        )
    finally:
        server.close()
        await server.wait_closed()
    assert result.success is True
    assert "accepted" in result.detail


@pytest.mark.asyncio
async def test_http_healthcheck(tmp_path: Path) -> None:
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self) -> None:  # noqa: N802
            self.send_response(204)
            self.end_headers()

        def log_message(self, format: str, *args: object) -> None:
            return

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    port = server.server_address[1]
    try:
        result = await HealthChecker().wait(
            HealthCheckConfig(
                type=HealthType.HTTP,
                url=f"http://127.0.0.1:{port}/health",
                expected_status=204,
                timeout=1.0,
                interval=0.01,
            ),
            tmp_path,
            {},
        )
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=1)
    assert result.success is True
    assert result.detail == "HTTP 204, expected 204"


@pytest.mark.asyncio
async def test_command_healthcheck(tmp_path: Path) -> None:
    result = await HealthChecker().wait(
        HealthCheckConfig(
            type=HealthType.COMMAND,
            command=[sys.executable, "-c", "print('ready')"],
            timeout=1.0,
            interval=0.01,
        ),
        tmp_path,
        {},
    )
    assert result.success is True
    assert "ready" in result.detail


@pytest.mark.asyncio
async def test_healthcheck_timeout_has_evidence(tmp_path: Path) -> None:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
        probe.bind(("127.0.0.1", 0))
        port = probe.getsockname()[1]
    result = await HealthChecker().wait(
        HealthCheckConfig(type=HealthType.TCP, port=port, timeout=0.05, interval=0.01),
        tmp_path,
        {},
    )
    assert result.success is False
    assert result.attempts >= 1
    assert "timeout" in result.detail

