# vigiaDev 🛡️⚡

[![Go Version](https://img.shields.io/badge/go-%3E%3D1.22-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![CI](https://github.com/Rafael-Albernaz-dev/vigiadev/actions/workflows/ci.yml/badge.svg)](https://github.com/Rafael-Albernaz-dev/vigiadev/actions)

> **Fast, deterministic, and safe local development environment orchestrator.**
> Native Go CLI with sub-millisecond boot, Kahn DAG dependency scheduling, deep monorepo auto-detection, Docker Compose integration, and an interactive Charm.sh TUI.

---

## 🌟 Highlights

- **⚡ Native Go Binary**: Standalone static binary (~12MB), zero runtime dependencies, instant boot (<2ms).
- **🧭 Deterministic DAG Orchestration**: Resolves service dependencies via Kahn's topological sort algorithm, running independent services in concurrent execution waves.
- **📦 Deep Monorepo & Modular Stack Detection**: Automatically discovers nested modular apps (`backend/apps/*`, `frontend/apps/*`, npm/pnpm/yarn/bun workspaces) with dedicated ports and execution directories (`dir:`).
- **🛡️ Strict Safety & Teardown Model**: Never runs destructive commands (`docker compose down`, `kill -9`) without explicit policy. Tracks every PID, PGID, and Container ID in `.vigiadev/run.json` and stops only resources owned by the current session.
- **🖥️ Interactive Charm.sh TUI**: Rich terminal interface powered by Bubble Tea, Lip Gloss, and Bubbles:
  - Full clickable and navigable URLs (`http://localhost:<port>`).
  - Real-time log streaming with search/filter (`[/]`).
  - Live system resource telemetry (CPU %, RAM, Disk I/O).
  - Instant on-demand lifecycle controls: `[s]` to Start/Stop, `[r]` to Restart.
  - Comprehensive diagnostics modal (`[d]`).
- **🔌 Intelligent Port Conflict Management**: Proactive detection of `EADDRINUSE` with non-invasive remediation tips, deterministic sequential remapping, and preflight conflict inspection (`[y/N/remap]`).
- **🏃 Task Runner with Keep-Alive**: Execute one-off commands (`vigiadev run <task>`) with automated topological dependency resolution, TCP readiness probes, and argument passthrough.

---

## 📥 Installation

### Option 1: Go Install (Recommended)

```bash
go install github.com/Rafael-Albernaz-dev/vigiadev/cmd/vigiadev@latest
```

### Option 2: Pre-compiled Binaries

Download the latest release for your platform (Linux/macOS, amd64/arm64) from [GitHub Releases](https://github.com/Rafael-Albernaz-dev/vigiadev/releases).

```bash
# Example for Linux x86_64:
curl -Lo vigiadev https://github.com/Rafael-Albernaz-dev/vigiadev/releases/latest/download/vigiadev_linux_amd64
chmod +x vigiadev
sudo mv vigiadev /usr/local/bin/
```

### Option 3: Build from Source

```bash
git clone https://github.com/Rafael-Albernaz-dev/vigiadev.git
cd vigiadev
go build -o bin/vigiadev ./cmd/vigiadev
sudo cp bin/vigiadev /usr/local/bin/
```

---

## 🚀 Quick Start

### 1. Initialize your project

Run in your project root to auto-detect Docker Compose, Node.js, Python, and Go stacks:

```bash
# Generates vigiadev.yaml (or vigiadev.local.yaml for gitignored local developer config)
vigiadev init

# Or explicitly create local machine overrides:
vigiadev init --local
```

### 2. Validate configuration

```bash
vigiadev validate
```

### 3. Spin up the environment

```bash
# Starts all services with the interactive TUI:
vigiadev up

# Start only a subset of services (dependencies are automatically resolved):
vigiadev up api frontend

# Run in headless / CI mode with stream logs:
vigiadev up --no-tui
```

### 4. Stop and Clean

```bash
# Graceful teardown of active session resources:
vigiadev down

# Full removal of session cache, lockfiles and optional configurations:
vigiadev remove
```

---

## ⌨️ TUI Keyboard & Mouse Navigation

When running `vigiadev up`, the interactive TUI provides full control:

| Key / Action | Description |
| :--- | :--- |
| **`Tab`** / **`Shift+Tab`** | Switch between tabs (`ALL`, individual services, `METRICS`) |
| **`Mouse Click`** | Click on any tab or status card row to jump directly |
| **`Mouse Wheel` / `↑ ↓`** | Scroll logs or metrics table smoothly |
| **`1` – `9`** | Quick jump directly to tab by index number |
| **`/`** | Activate real-time case-insensitive log search and filter |
| **`Esc`** | Clear filter or dismiss modal |
| **`s`** | **Start / Stop** the active service on-demand (toggles running state) |
| **`r`** | **Restart** the active service or container cleanly |
| **`c`** | Clear logs in the current active tab |
| **`d`** | Open **Diagnostics & System Doctor** modal |
| **`q`** / **`Ctrl+C`** | Gracefully terminate all session processes and exit |

---

## 📋 CLI Commands

| Command | Description |
| :--- | :--- |
| `vigiadev init [--local] [-f]` | Inspects project repository and generates configuration |
| `vigiadev up [services...]` | Orchestrates and boots services with interactive TUI or streaming |
| `vigiadev run <task> [--no-deps]` | Executes a declared task resolving prerequisite dependencies |
| `vigiadev down` | Shuts down processes and containers owned by the active session |
| `vigiadev remove [-f] [--keep-config]` | Removes `.vigiadev/` runtime data, locks, and optionally configs |
| `vigiadev logs [service] [-f] [-n]` | Inspects or tails persisted log files in `.vigiadev/logs/` |
| `vigiadev doctor` | Audits system requirements (Docker, Compose, ports, permissions) |
| `vigiadev validate [-c config]` | Lints and validates configuration file and DAG topological cycles |
| `vigiadev version` | Displays CLI version, Git commit hash, and build date |

---

## ⚙️ Configuration Shape (`vigiadev.yaml`)

Configuration lookup order: `--config` flag ➔ `vigiadev.local.yaml` ➔ `vigiadev.yaml` ➔ legacy `dev.yaml`.

```yaml
version: 1
project_name: my-app

services:
  # Docker Compose integration
  postgres:
    compose_service: postgres
    ports: [5432]
    port_policy: reuse
    healthcheck:
      type: tcp
      port: 5432

  redis:
    compose_service: redis
    ports: [6379]
    port_policy: reuse

  # Host process in a modular sub-directory
  backend:
    dir: backend
    command: ["npm", "run", "start:dev"]
    depends_on: [postgres, redis]
    ports: [3000]
    port_policy: remap
    watch: true
    watch_paths: ["src/"]
    healthcheck:
      type: http
      url: "http://127.0.0.1:3000/health"
      expected_status: 200

  # Frontend app
  frontend:
    dir: frontend
    command: ["npm", "run", "dev"]
    depends_on: [backend]
    ports: [5173]
    port_policy: remap
    healthcheck:
      type: tcp
      port: 5173

tasks:
  migrate:
    dir: backend
    command: ["npm", "run", "typeorm", "migration:run"]
    depends_on: [postgres]

  test:
    command: ["npm", "test"]
    depends_on: [backend]
```

---

## 🔒 Safety & Resource Ownership

vigiaDev follows the principle of **strict ownership**:
1. **Never destructive by default**: Does not call `docker compose down` or kill external processes unless explicitly instructed via policies or `--kill-ports`.
2. **Session Ledger (`.vigiadev/run.json`)**: Every spawned process group (`PGID`), PID, and started Docker Container ID is atomically recorded.
3. **Surgical Teardown**: Shutdown targets **strictly** the resources recorded in the session ledger, ensuring databases, daemons, or sibling projects remain untouched.
4. **Stale Lock Recovery**: `.vigiadev/vigiadev.lock` uses PID-liveness validation to safely clean up orphaned locks if an earlier session crashed.

---

## 🧪 Testing & Verification

vigiaDev is covered by comprehensive unit, integration, and architecture tests:

```bash
# Run full deterministic test suite:
go test -count=1 ./...

# Run static analysis:
go vet ./...
```

---

## 📄 License

Distributed under the MIT License. See [LICENSE](LICENSE) for more information.
