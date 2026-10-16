# vigiaDev 🛡️⚡

[![Release](https://img.shields.io/badge/version-v1.0.0-blue.svg)](https://github.com/Rafael-Albernaz-dev/vigiadev/releases)
[![Go Version](https://img.shields.io/badge/go-%3E%3D1.22-blue.svg)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![CI](https://github.com/Rafael-Albernaz-dev/vigiadev/actions/workflows/ci.yml/badge.svg)](https://github.com/Rafael-Albernaz-dev/vigiadev/actions)

> **Fast, deterministic, and safe local development environment orchestrator.**
> Native Go CLI with sub-millisecond boot, Kahn DAG dependency scheduling, deep monorepo auto-detection, Docker Compose integration, and an interactive Charm.sh TUI.
> 
> 📌 **Status: v1.0.0 (Feature-Complete & Stable)**  
> vigiaDev is in General Availability (v1.0.0). The core architecture and feature set are frozen and validated for production monorepo environments. Going forward, development is strictly focused on stability, reliability, and bug fixes.

---

## 🌟 Highlights

- **⚡ Native Go Binary**: Standalone static binary (~12MB), zero runtime dependencies, instant boot (<2ms).
- **🧭 Deterministic DAG Orchestration**: Resolves service dependencies via Kahn's topological sort algorithm, running independent services in concurrent execution waves.
- **📦 Deep Monorepo & Modular Stack Detection**: Automatically discovers nested modular apps (`backend/apps/*`, `frontend/apps/*`, npm/pnpm/yarn/bun workspaces) with dedicated execution directories (`dir:`). Interspects real backend ports from Vite proxy configs, manifests, or source code, automatically wires DAG dependencies (`depends_on: [<module>-backend]`), and synthesizes declarative execution profiles.
- **🛡️ Strict Safety & Teardown Model**: Never runs destructive commands (`docker compose down`, `kill -9`) without explicit policy. Tracks every PID, PGID, working directory, and Container ID in `.vigiadev/run.json` with immediate spawn persistence. Automatically cleans up stale/orphaned processes across subdirectories on `vigiadev up` while preserving external workloads.
- **🖥️ Interactive Charm.sh TUI**: Rich terminal interface powered by Bubble Tea, Lip Gloss, and Bubbles:
  - Native terminal clickable hyperlinks via **ANSI OSC 8** (`http://localhost:<port>`) supporting direct Ctrl+Click in modern terminals.
  - Semantic port presentation: active web services display navigable hyperlinks, while databases and brokers display raw listening sockets (`:5432`, `:6379`).
  - Real-time log streaming with search/filter (`[/]`).
  - Live system resource telemetry (CPU %, RAM, Disk I/O).
  - Instant on-demand lifecycle controls: `[s]` / `[x]` to Start/Stop selected service, `[a]` to Start All stopped/failed services, `[r]` to Restart.
  - Comprehensive diagnostics modal (`[d]`).
- **🎯 Execution Profiles & Select-All (`profiles:` / `-a`)**: Declare named service groups (e.g., `profiles: { erp: [...], platform: [...] }`) and boot targeted sub-graphs with `vigiadev up -p <name>`, interactive terminal selector, or bypass the prompt to boot everything with `vigiadev up -a` (`-A` / `--all`).
- **🔍 Live Session & Port Status (`vigiadev status` / `ps`)**: Inspect whether an active session is already running (supervisor PID, `RunID`, per-service `PID`/`PGID` and containers) and detect external port conflicts before starting.
- **📂 Upward Config Discovery for Modular Monoliths**: Run `vigiadev up`, `status`, `down`, `run`, or `logs` from any nested module directory (e.g., `erp/backend`); vigiaDev automatically walks upward to locate `vigiadev.local.yaml` / `vigiadev.yaml` and anchors paths to the project root.
- **📄 Layered Environment Hierarchy (`.env`)**: Strict 6-layer precedence merging (`.env` ➔ `.env.local` ➔ `<dir>/.env` ➔ `<dir>/.env.local` ➔ `env_file:` ➔ `env:`), isolating modular credentials cleanly.
- **🐳 Docker Stale Container Auto-Remediation**: Automatically recovers from Docker name conflicts by removing stopped/dead stale containers, while protecting actively running containers.
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
make build
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

### 2. Validate & Inspect Status

```bash
# Validate configuration and DAG dependencies:
vigiadev validate

# Check if a session is already running and inspect service ports before starting:
vigiadev status
# (or shorthand: vigiadev ps)
```

### 3. Spin up the environment

```bash
# Starts services with interactive TUI (detects active sessions & presents profile selector):
vigiadev up

# Start ALL declared services directly (bypasses profile prompt):
vigiadev up -a
# (also accepts -A or --all)

# Boot a specific profile directly:
vigiadev up --profile erp
vigiadev up -p viabilidade

# Start only a subset of services (dependencies are automatically resolved):
vigiadev up api frontend

# Replace an already active session and start all services without prompting:
vigiadev up -a --force

# Run in headless / CI mode with stream logs:
vigiadev up --no-tui
```

### 4. Stop and Clean

```bash
# Graceful teardown of active session resources (services, process groups, and supervisor):
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
| **`↑` / `↓`** | Select service in the status header (when on `ALL` tab) or scroll logs/metrics |
| **`Mouse Click`** | Click on any tab or status card row to jump directly |
| **`Mouse Wheel`** | Scroll logs or metrics table smoothly |
| **`1` – `9`** | Quick jump directly to tab by index number |
| **`/`** | Activate real-time case-insensitive log search and filter |
| **`Esc`** | Clear filter or dismiss modal |
| **`s`** / **`x`** | **Start / Stop** the selected or active service on-demand |
| **`a`** / **`A`** | **Start All** stopped or failed services in topological dependency order |
| **`r`** | **Restart** the selected or active service or container cleanly |
| **`c`** | Clear logs in the current active tab |
| **`d`** | Open **Diagnostics & System Doctor** modal |
| **`q`** / **`Ctrl+C`** | Gracefully terminate all session processes and exit |

---

## 📋 CLI Commands

| Command | Description |
| :--- | :--- |
| `vigiadev init [--local] [-f]` | Inspects project repository and generates configuration |
| `vigiadev up [services...] [-a] [-p profile] [-f]` | Orchestrates and boots services (all or profile subset) with interactive TUI or streaming |
| `vigiadev status` (aliases: `ps`, `st`) | Inspects active session, supervisor PID, service states, and port conflicts |
| `vigiadev run <task> [--no-deps]` | Executes a declared task resolving prerequisite dependencies |
| `vigiadev down` | Shuts down processes, process groups, containers, and supervisor owned by the active session |
| `vigiadev remove [-f] [--keep-config]` | Removes `.vigiadev/` runtime data, locks, and optionally configs |
| `vigiadev logs [service] [-f] [-n]` | Inspects or tails persisted log files in `.vigiadev/logs/` |
| `vigiadev doctor` | Audits system requirements (Docker, Compose, ports, permissions) |
| `vigiadev validate [-c config]` | Lints and validates configuration file and DAG topological cycles |
| `vigiadev version` | Displays CLI version, Git commit hash, and build date |

---

## ⚙️ Configuration Shape (`vigiadev.yaml`)

Configuration lookup order (with automatic upward directory traversal from subdirectories): `--config` flag ➔ `vigiadev.local.yaml` ➔ `vigiadev.yaml` ➔ legacy `dev.yaml`.

```yaml
version: 1
project_name: my-app

# Declarative execution profiles
profiles:
  api: [postgres, redis, backend]
  full: [postgres, redis, backend, frontend]

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

  # Host process in a modular sub-directory with layered env
  backend:
    dir: backend
    command: ["npm", "run", "start:dev"]
    depends_on: [postgres, redis]
    ports: [3000]
    port_policy: remap
    env_file: .env.local
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
2. **Session Ledger (`.vigiadev/run.json`)**: Every spawned process group (`PGID`), PID, working directory (`dir`), and started Docker Container ID is recorded immediately on spawn.
3. **Surgical Teardown**: Shutdown targets **strictly** the resources recorded in the session ledger, ensuring databases, daemons, or sibling projects remain untouched.
4. **Active Session Guard & Stale Lock Recovery**: `.vigiadev/vigiadev.lock` validates process liveness (filtering out zombie states in `/proc/<pid>/stat`) to prevent duplicate concurrent sessions while automatically recovering from crashed locks.

---

## 🧪 Testing, Regression Suite & Quality Gate

vigiaDev enforces a full **Quality Gate** covering static analysis, race-detector unit/integration tests, dedicated regression tests (`test/regression/`), binary build, and modular monolith smoke testing:

```bash
# Run the complete Quality Gate (vet + race tests + build + smoke test):
make quality-gate

# Run the dedicated regression & integration test suite with race detector:
make test-regression

# Run full test suite with race detector:
go test -v -race ./...
```

---

## 📄 License

Distributed under the MIT License. See [LICENSE](LICENSE) for more information.
