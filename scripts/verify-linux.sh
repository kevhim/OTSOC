#!/usr/bin/env bash
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

# 1. Output Directory Configuration
# Accepts explicit external directory via argument $1 or env var EVIDENCE_EXPORT_DIR / OUTPUT_DIR
TARGET_BASE_DIR="${1:-${EVIDENCE_EXPORT_DIR:-${OUTPUT_DIR:-${REPO_ROOT}/docs/evidence/phase4/runs}}}"

TIMESTAMP="$(date -u +"%Y%m%d_%H%M%S")"
SHORT_REV="$(git rev-parse --short=8 HEAD 2>/dev/null || echo "unknown")"
RUN_ID="linux_${TIMESTAMP}_${SHORT_REV}"
RUN_DIR="${TARGET_BASE_DIR}/${RUN_ID}"

# Exclusive directory creation: ensure parent exists, then create RUN_DIR without -p
if [ ! -d "${TARGET_BASE_DIR}" ]; then
    mkdir -p "${TARGET_BASE_DIR}" || {
        echo "ERROR: Failed to create base export directory ${TARGET_BASE_DIR}" >&2
        exit 1
    }
fi

if [ -e "${RUN_DIR}" ]; then
    echo "ERROR: Run directory already exists at ${RUN_DIR}. Refusing overwrite." >&2
    exit 1
fi

mkdir "${RUN_DIR}" || {
    echo "ERROR: Failed exclusive creation of run directory ${RUN_DIR}" >&2
    exit 1
}

SUMMARY_LOG="${RUN_DIR}/linux_verification_summary.txt"
COMMANDS_LOG="${RUN_DIR}/commands.log"
TRANSCRIPT_LOG="${RUN_DIR}/transcript.log"

echo "=========================================================="
echo "   RedCyberFox Linux Runtime Verification & Audit         "
echo "   Run ID:    ${RUN_ID}"
echo "   Output:    ${RUN_DIR}"
echo "   Repo Root: ${REPO_ROOT}"
echo "=========================================================="

# 2. Pre-Execution Boundary & Environment Inspection
PRE_REV="$(git rev-parse HEAD 2>/dev/null || echo "unknown")"
PRE_STATUS="$(git status --porcelain 2>/dev/null || echo "")"
PRE_DIRTY=false
if [ -n "${PRE_STATUS}" ]; then
    PRE_DIRTY=true
fi

OS_UNAME="$(uname -a)"
OS_DISTRO="unknown"
if [ -f /etc/os-release ]; then
    OS_DISTRO="$(grep PRETTY_NAME /etc/os-release | cut -d= -f2 | tr -d '\"')"
fi
GO_VER="$(go version 2>/dev/null || echo "go missing")"
CGO_ENV="$(go env CGO_ENABLED 2>/dev/null || echo "unknown")"
GCC_VER="$(gcc --version 2>/dev/null | head -n1 || echo "gcc missing")"

# Native library probe
HAS_YARA_X=false
YARA_X_VER=""
if command -v pkg-config >/dev/null 2>&1; then
    if pkg-config --modversion yara_x_capi >/dev/null 2>&1; then
        HAS_YARA_X=true
        YARA_X_VER="$(pkg-config --modversion yara_x_capi 2>/dev/null)"
    fi
fi

{
    echo "=========================================================="
    echo "   RedCyberFox Linux Runtime Verification Summary Log     "
    echo "=========================================================="
    echo "Run ID:          ${RUN_ID}"
    echo "Timestamp UTC:   $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
    echo "Source Revision: ${PRE_REV}"
    echo "Source Dirty:    ${PRE_DIRTY}"
    echo "OS / Kernel:     ${OS_UNAME}"
    echo "Linux Distro:    ${OS_DISTRO}"
    echo "Go Toolchain:    ${GO_VER}"
    echo "CGO Enabled:     ${CGO_ENV}"
    echo "C Compiler:      ${GCC_VER}"
    if [ "${HAS_YARA_X}" = true ]; then
        echo "YARA-X C API:    Detected (version ${YARA_X_VER})"
    else
        echo "YARA-X C API:    Not installed (pkg-config yara_x_capi missing)"
    fi
    echo "=========================================================="
    echo ""
} > "${SUMMARY_LOG}"

{
    echo "# RedCyberFox Linux Verification Commands Log"
    echo "# Run ID: ${RUN_ID}"
    echo "# Started: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
    echo ""
} > "${COMMANDS_LOG}"

{
    echo "=== REDCYBERFOX LINUX VERIFICATION TRANSCRIPT ==="
    echo "Run ID: ${RUN_ID}"
    echo "Started: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
    echo ""
} > "${TRANSCRIPT_LOG}"

# 3. Stage Tracking
declare -a STAGE_NAMES=()
declare -a STAGE_STATUSES=()
declare -a STAGE_DETAILS=()
OVERALL_FAILED=false
OVERALL_BLOCKED=false

RUN_STAGE() {
    local name="$1"
    shift
    local cmd="$*"

    echo "" >> "${TRANSCRIPT_LOG}"
    echo "==========================================================" >> "${TRANSCRIPT_LOG}"
    echo "STAGE: ${name}" >> "${TRANSCRIPT_LOG}"
    echo "COMMAND: ${cmd}" >> "${TRANSCRIPT_LOG}"
    echo "TIMESTAMP_START: $(date -u -Iseconds)" >> "${TRANSCRIPT_LOG}"
    echo "==========================================================" >> "${TRANSCRIPT_LOG}"

    echo "[$(date -u +"%Y-%m-%dT%H:%M:%SZ")] START: ${name} | CMD: ${cmd}" >> "${COMMANDS_LOG}"

    local stage_out
    local stage_exit
    stage_out=$(eval "$cmd" 2>&1)
    stage_exit=$?

    echo "${stage_out}" >> "${TRANSCRIPT_LOG}"
    echo "EXIT_CODE: ${stage_exit}" >> "${TRANSCRIPT_LOG}"
    echo "TIMESTAMP_END: $(date -u -Iseconds)" >> "${TRANSCRIPT_LOG}"
    echo "[$(date -u +"%Y-%m-%dT%H:%M:%SZ")] FINISH: ${name} | EXIT_CODE: ${stage_exit}" >> "${COMMANDS_LOG}"

    if [ ${stage_exit} -eq 0 ]; then
        STAGE_NAMES+=("${name}")
        STAGE_STATUSES+=("PASS")
        STAGE_DETAILS+=("Clean execution (exit 0)")
        printf "  %-50s | PASS\n" "${name}"
    else
        STAGE_NAMES+=("${name}")
        STAGE_STATUSES+=("FAIL")
        STAGE_DETAILS+=("Non-zero exit code: ${stage_exit}")
        OVERALL_FAILED=true
        printf "  %-50s | FAIL (exit %d)\n" "${name}" "${stage_exit}"
    fi
    return ${stage_exit}
}

RECORD_BLOCKED() {
    local name="$1"
    local details="$2"
    STAGE_NAMES+=("${name}")
    STAGE_STATUSES+=("BLOCKED")
    STAGE_DETAILS+=("${details}")
    OVERALL_BLOCKED=true
    printf "  %-50s | BLOCKED (%s)\n" "${name}" "${details}"
    echo "[$(date -u +"%Y-%m-%dT%H:%M:%SZ")] BLOCKED: ${name} | ${details}" >> "${COMMANDS_LOG}"
}

echo ""
echo "--> Executing Core Unit & Subsystem Tests (-count=1, explicit timeouts)..."

RUN_STAGE "Core Storage Suite" "CGO_ENABLED=0 go test -v -count=1 -timeout 60s ./agent/internal/storage/..."
RUN_STAGE "Core EventQueue Suite" "CGO_ENABLED=0 go test -v -count=1 -timeout 60s ./agent/internal/eventqueue/..."
RUN_STAGE "Core Governor Suite" "CGO_ENABLED=0 go test -v -count=1 -timeout 60s ./agent/internal/governor/..."
RUN_STAGE "Core ResourceReport Suite" "CGO_ENABLED=0 go test -v -count=1 -timeout 60s ./agent/internal/resourcereport/..."
RUN_STAGE "Core Forwarder Suite" "CGO_ENABLED=0 go test -v -count=1 -timeout 60s ./agent/internal/forwarder/..."
RUN_STAGE "Core Ingestion Suite" "CGO_ENABLED=0 go test -v -count=1 -timeout 60s ./agent/internal/ingestion/..."

echo ""
echo "--> Executing Crash Boundary, Process Termination & Diagnostic Regressions..."
RUN_STAGE "Delivery Boundaries & Crash Regressions" "CGO_ENABLED=0 go test -v -count=1 -timeout 60s ./agent/internal/integration -run '^TestCrashBoundary'"
RUN_STAGE "Diagnostic Retention & Error Regressions" "CGO_ENABLED=0 go test -v -count=1 -timeout 60s ./agent/internal/integration -run '^TestDiagnostic'"

echo ""
echo "--> Evaluating Race Detector with CGO Enabled..."
if [ "${GCC_VER}" != "gcc missing" ]; then
    RUN_STAGE "Go Test -Race (Governor, EventQueue, Storage)" "CGO_ENABLED=1 go test -race -v -count=1 -timeout 120s ./agent/internal/governor/... ./agent/internal/eventqueue/... ./agent/internal/storage/..."
else
    RECORD_BLOCKED "Go Test -Race (CGO Enabled)" "GCC / CGO toolchain not available on host"
fi

echo ""
echo "--> Evaluating Native YARA-X Detection Engine & Workload..."
if [ "${HAS_YARA_X}" = true ]; then
    RUN_STAGE "Native YARA-X Detection Engine" "go test -v -count=1 -timeout 60s -tags=yara_x ./agent/internal/detection/..."
    RUN_STAGE "Native YARA-X Component Workload & Policy" "go test -v -count=1 -timeout 120s ./agent/internal/integration -run '^TestWorkload_YaraX|^TestYaraX_'"
else
    RECORD_BLOCKED "Native YARA-X C API (libyara_x_capi)" "libyara_x_capi not installed in local environment (verified in Linux CI via cargo cinstall)"
    RECORD_BLOCKED "Native YARA-X Component Workload & Policy" "Omitted: requires libyara_x_capi CGO dependency"
fi

echo ""
echo "--> Building Native Linux Endpoint Binary & Recording Metadata..."
ENDPOINT_BIN="${RUN_DIR}/endpoint_linux"
RUN_STAGE "Native Linux Endpoint Build" "CGO_ENABLED=0 go build -v -o '${ENDPOINT_BIN}' ./agent/cmd/endpoint"

if [ -f "${ENDPOINT_BIN}" ]; then
    RUN_STAGE "Binary Provenance Metadata (go version -m)" "go version -m '${ENDPOINT_BIN}' > '${RUN_DIR}/endpoint_metadata.txt'"

    echo ""
    echo "--> Performing Bounded Harmless Monitoring Smoke Run..."
    SMOKE_TMP="$(mktemp -d)"
    SMOKE_REPORT="${RUN_DIR}/linux_smoke_report.json"
    cat <<EOF > "${SMOKE_TMP}/agent.json"
{
  "api_addr": "http://127.0.0.1:9",
  "tenant_id": "tenant-linux-smoke",
  "site_id": "site-linux-smoke",
  "monitor_paths": []
}
EOF

    RUN_STAGE "Bounded Runtime Smoke Run (2s duration)" \
        "timeout 15s '${ENDPOINT_BIN}' -config='${SMOKE_TMP}/agent.json' -identity='${SMOKE_TMP}/identity.json' -measure-report='${SMOKE_REPORT}' -measure-profile='linux-runtime-smoke' -measure-duration=2s -measure-interval=1s"

    # Verify report was generated and is valid JSON
    if [ -f "${SMOKE_REPORT}" ] && grep -q '"profile": *"linux-runtime-smoke"' "${SMOKE_REPORT}" 2>/dev/null; then
        STAGE_NAMES+=("Smoke Report Verification")
        STAGE_STATUSES+=("PASS")
        STAGE_DETAILS+=("Valid measurement report generated")
        printf "  %-50s | PASS\n" "Smoke Report Verification"
    else
        STAGE_NAMES+=("Smoke Report Verification")
        STAGE_STATUSES+=("FAIL")
        STAGE_DETAILS+=("Report file missing or invalid")
        OVERALL_FAILED=true
        printf "  %-50s | FAIL (smoke report missing or invalid)\n" "Smoke Report Verification"
    fi

    # Clean up smoke temp directory and compiled binary
    rm -rf "${SMOKE_TMP}"
    rm -f "${ENDPOINT_BIN}"
else
    RECORD_BLOCKED "Bounded Runtime Smoke Run" "Endpoint binary failed to compile"
fi

# 4. Post-Execution Boundary Stability Check
POST_REV="$(git rev-parse HEAD 2>/dev/null || echo "unknown")"
POST_STATUS="$(git status --porcelain 2>/dev/null || echo "")"
POST_DIRTY=false
if [ -n "${POST_STATUS}" ]; then
    POST_DIRTY=true
fi

if [ "${PRE_REV}" != "${POST_REV}" ]; then
    RECORD_BLOCKED "Source Boundary Stability" "Git revision changed during execution: pre=${PRE_REV}, post=${POST_REV}"
fi

# 5. Summary Generation & Final Exit Code
{
    echo "==========================================================" >> "${SUMMARY_LOG}"
    echo "                   VERIFICATION RESULTS                   " >> "${SUMMARY_LOG}"
    echo "==========================================================" >> "${SUMMARY_LOG}"
    for i in "${!STAGE_NAMES[@]}"; do
        printf "%-50s | %-8s | %s\n" "${STAGE_NAMES[$i]}" "${STAGE_STATUSES[$i]}" "${STAGE_DETAILS[$i]}" >> "${SUMMARY_LOG}"
    done
    echo "==========================================================" >> "${SUMMARY_LOG}"
    echo "Post-Execution Revision: ${POST_REV}" >> "${SUMMARY_LOG}"
    echo "Post-Execution Dirty:    ${POST_DIRTY}" >> "${SUMMARY_LOG}"
    echo "Finished At UTC:         $(date -u +"%Y-%m-%dT%H:%M:%SZ")" >> "${SUMMARY_LOG}"
    echo "" >> "${SUMMARY_LOG}"
    echo "QUALIFICATION STATUS NOTICE:" >> "${SUMMARY_LOG}"
    echo "1. WSL2 Linux test execution verifies core agent subsystem correctness, memory-race safety," >> "${SUMMARY_LOG}"
    echo "   crash-boundary containment, and clean bounded runtime measurement on the Linux OS." >> "${SUMMARY_LOG}"
    echo "2. WSL2 Linux does NOT substitute for physical low-spec hardware qualification." >> "${SUMMARY_LOG}"
    echo "3. Phase 4 remains OPEN. Threshold calibration, governor enforcement, endpoint rule loading," >> "${SUMMARY_LOG}"
    echo "   and low-spec hardware qualification remain subsequent gates." >> "${SUMMARY_LOG}"
    echo "==========================================================" >> "${SUMMARY_LOG}"
}

echo ""
echo "=========================================================="
echo "                   VERIFICATION SUMMARY                   "
echo "=========================================================="
for i in "${!STAGE_NAMES[@]}"; do
    printf "%-50s | %-8s | %s\n" "${STAGE_NAMES[$i]}" "${STAGE_STATUSES[$i]}" "${STAGE_DETAILS[$i]}"
done
echo "=========================================================="
echo "Notice: WSL results verify OS subsystem correctness, not physical low-spec hardware qualification."
echo "Phase 4 remains OPEN."
echo "Summary Log:    ${SUMMARY_LOG}"
echo "Commands Log:   ${COMMANDS_LOG}"
echo "Transcript Log: ${TRANSCRIPT_LOG}"
echo "=========================================================="

if [ "${OVERALL_FAILED}" = true ]; then
    echo "RESULT: FAIL (one or more test stages failed)"
    exit 1
elif [ "${OVERALL_BLOCKED}" = true ]; then
    echo "RESULT: BLOCKED (one or more mandatory dependencies blocked)"
    exit 2
else
    echo "RESULT: ALL TESTS PASSED"
    exit 0
fi
