from __future__ import annotations

import asyncio
import os
import signal
import time
import urllib.error
import urllib.request
from pathlib import Path

from vigiadev.application.ports import HealthResult
from vigiadev.domain.models import HealthCheckConfig, HealthType


class HealthChecker:
    async def wait(
        self, config: HealthCheckConfig, cwd: Path, env: dict[str, str]
    ) -> HealthResult:
        loop = asyncio.get_running_loop()
        deadline = loop.time() + float(config.timeout)
        attempts = 0
        last_detail = "healthcheck did not run"
        last_latency = 0.0

        while True:
            attempts += 1
            remaining = max(0.001, deadline - loop.time())
            started = time.perf_counter()
            success, detail = await self._once(config, cwd, env, remaining)
            last_latency = (time.perf_counter() - started) * 1000
            last_detail = detail
            if success:
                return HealthResult(True, last_latency, detail, attempts)
            remaining = deadline - loop.time()
            if remaining <= 0:
                return HealthResult(
                    False,
                    last_latency,
                    f"timeout after {attempts} attempt(s): {last_detail}",
                    attempts,
                )
            await asyncio.sleep(min(float(config.interval), remaining))

    async def _once(
        self,
        config: HealthCheckConfig,
        cwd: Path,
        env: dict[str, str],
        timeout: float,
    ) -> tuple[bool, str]:
        try:
            if config.type is HealthType.TCP:
                return await self._tcp(config, timeout)
            if config.type is HealthType.HTTP:
                return await self._http(config, timeout)
            return await self._command(config, cwd, env, timeout)
        except TimeoutError:
            return False, "attempt timed out"
        except (OSError, urllib.error.URLError) as error:
            return False, str(error)

    @staticmethod
    async def _tcp(config: HealthCheckConfig, timeout: float) -> tuple[bool, str]:
        assert config.port is not None
        reader: asyncio.StreamReader
        writer: asyncio.StreamWriter
        reader, writer = await asyncio.wait_for(
            asyncio.open_connection(config.host, config.port), timeout=timeout
        )
        del reader
        writer.close()
        await writer.wait_closed()
        return True, f"tcp://{config.host}:{config.port} accepted a connection"

    @staticmethod
    async def _http(config: HealthCheckConfig, timeout: float) -> tuple[bool, str]:
        assert config.url is not None

        def request() -> int:
            try:
                with urllib.request.urlopen(config.url, timeout=timeout) as response:
                    return response.status
            except urllib.error.HTTPError as error:
                return error.code

        status = await asyncio.wait_for(asyncio.to_thread(request), timeout=timeout + 0.1)
        success = status == config.expected_status
        return success, f"HTTP {status}, expected {config.expected_status}"

    @staticmethod
    async def _command(
        config: HealthCheckConfig,
        cwd: Path,
        env: dict[str, str],
        timeout: float,
    ) -> tuple[bool, str]:
        assert config.command is not None
        merged_env = os.environ.copy()
        merged_env.update(env)
        process = await asyncio.create_subprocess_exec(
            *config.command,
            cwd=cwd,
            env=merged_env,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            preexec_fn=os.setpgrp,
        )
        try:
            stdout, stderr = await asyncio.wait_for(process.communicate(), timeout=timeout)
        except TimeoutError:
            try:
                os.killpg(process.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            await process.wait()
            raise
        detail_bytes = stdout.strip() or stderr.strip()
        detail = detail_bytes.decode(errors="replace") if detail_bytes else "no output"
        return process.returncode == 0, f"exit {process.returncode}: {detail}"

