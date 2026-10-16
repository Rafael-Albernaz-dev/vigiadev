#!/usr/bin/env bash
set -euo pipefail

BIN_PATH="./bin/vigiadev"
if [ ! -f "$BIN_PATH" ]; then
    echo "❌ Binary $BIN_PATH not found. Run 'make build' first."
    exit 1
fi

echo "🔍 [Smoke Test] 1. Validando comandos base e help..."
"$BIN_PATH" --help > /dev/null
"$BIN_PATH" up --help | grep -E -- "--all|-a" > /dev/null
echo "✅ Flags --all / -a registradas com sucesso."

echo "🔍 [Smoke Test] 2. Criando fixture do monolito modular (estilo atlas)..."
TMP_DIR=$(mktemp -d -t vigia-atlas-smoke-XXXXXX)
trap 'rm -rf "$TMP_DIR"' EXIT

mkdir -p "$TMP_DIR/backend" "$TMP_DIR/frontend" "$TMP_DIR/.vigiadev"

cat << 'EOF' > "$TMP_DIR/vigiadev.local.yaml"
version: 1
project_name: atlas-smoke
profiles:
  erp:
    - backend-svc
services:
  backend-svc:
    dir: backend
    command:
      - echo
      - backend-ok
tasks:
  check-task:
    dir: backend
    command:
      - echo
      - task-completed
EOF

echo "🔍 [Smoke Test] 3. Executando validação a partir da raiz da fixture..."
(
    cd "$TMP_DIR"
    "$OLDPWD/$BIN_PATH" validate > /dev/null
)
echo "✅ Validação na raiz da fixture concluída com sucesso."

echo "🔍 [Smoke Test] 4. Executando validação a partir do subdiretório backend/ (descoberta ascendente)..."
(
    cd "$TMP_DIR/backend"
    "$OLDPWD/$BIN_PATH" validate > /dev/null
)
echo "✅ Descoberta ascendente a partir do subdiretório validada com sucesso."

echo "🔍 [Smoke Test] 5. Executando task a partir do subdiretório backend/..."
(
    cd "$TMP_DIR/backend"
    OUTPUT=$("$OLDPWD/$BIN_PATH" run check-task)
    echo "$OUTPUT" | grep "task-completed" > /dev/null
)
echo "✅ Execução de task no contexto do monolito modular validada com sucesso."

echo "🔍 [Smoke Test] 6. Executando vigiadev status a partir do subdiretório backend/..."
(
    cd "$TMP_DIR/backend"
    STATUS_OUT=$("$OLDPWD/$BIN_PATH" status)
    echo "$STATUS_OUT" | grep "atlas-smoke" > /dev/null
    echo "$STATUS_OUT" | grep "Session: STOPPED" > /dev/null
)
echo "✅ Comando vigiadev status validado com sucesso."

echo "✨ [Smoke Test] Todos os testes de fumaça contra a arquitetura do atlas passaram 100%!"
