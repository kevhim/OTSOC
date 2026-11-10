#!/usr/bin/env bash
set -e
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
OUT="docs/evidence/phase4/linux_runtime_verification.txt"

echo "=== LINUX RUNTIME VERIFICATION (WSL2 Ubuntu) ===" > "$OUT"
echo "Timestamp: $(date -u -Iseconds)" >> "$OUT"
echo "Kernel: $(uname -a)" >> "$OUT"
echo "Go Version: $(go version)" >> "$OUT"
echo "Git HEAD: $(git rev-parse HEAD)" >> "$OUT"
echo "Git Status: $(git status --short)" >> "$OUT"
echo "" >> "$OUT"

echo "--- 1. Storage Suite ---" >> "$OUT"
CGO_ENABLED=0 go test -v ./agent/internal/storage/... >> "$OUT" 2>&1

echo "--- 2. EventQueue Suite ---" >> "$OUT"
CGO_ENABLED=0 go test -v ./agent/internal/eventqueue/... >> "$OUT" 2>&1

echo "--- 3. Governor Suite ---" >> "$OUT"
CGO_ENABLED=0 go test -v ./agent/internal/governor/... >> "$OUT" 2>&1

echo "--- 4. ResourceReport Suite ---" >> "$OUT"
CGO_ENABLED=0 go test -v ./agent/internal/resourcereport/... >> "$OUT" 2>&1

echo "--- 5. Forwarder Suite ---" >> "$OUT"
CGO_ENABLED=0 go test -v ./agent/internal/forwarder/... >> "$OUT" 2>&1

echo "--- 6. Ingestion Suite ---" >> "$OUT"
CGO_ENABLED=0 go test -v ./agent/internal/ingestion/... >> "$OUT" 2>&1

echo "--- 7. Crash Boundary & Diagnostic Regressions ---" >> "$OUT"
CGO_ENABLED=0 go test -v ./agent/internal/integration -run 'TestDiagnostic|TestCrashBoundary' >> "$OUT" 2>&1

echo "--- 8. Native Linux Endpoint Binary Build ---" >> "$OUT"
CGO_ENABLED=0 go build -v -o /tmp/linux_endpoint ./agent/cmd/endpoint >> "$OUT" 2>&1
/tmp/linux_endpoint -version >> "$OUT" 2>&1 || true
rm -f /tmp/linux_endpoint

echo "--- 9. Native YARA-X C API Dependency Probe ---" >> "$OUT"
if pkg-config --modversion yara_x_capi >> "$OUT" 2>&1; then
    echo "libyara_x_capi detected in WSL" >> "$OUT"
else
    echo "libyara_x_capi not installed in local WSL environment; compiled from source via cargo-c in Linux CI (ubuntu-latest)" >> "$OUT"
fi

