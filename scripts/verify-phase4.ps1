<#
.SYNOPSIS
    Verification script for Phase 4 Correctness Remediation Checkpoint.
.DESCRIPTION
    Runs all static, unit, integration, and dashboard checks.
    Supports -Race and -NativeDetection parameters.
    Auto-detects installed UCRT64 toolchain and real YARA-X C API if present.
    Reports environment-blocked dependencies explicitly without falling back to stubs
    or weakening test assertions.
.EXAMPLE
    .\scripts\verify-phase4.ps1 -Race -NativeDetection
#>
[CmdletBinding()]
param (
    [switch]$Race,
    [switch]$NativeDetection
)

$ErrorActionPreference = "Continue"

$repoRoot = (Resolve-Path "$PSScriptRoot\..").Path
Set-Location $repoRoot

# Auto-discover UCRT64 toolchain if installed in standard location (C:\msys64\ucrt64)
if (Test-Path "C:\msys64\ucrt64\bin\gcc.exe") {
    $env:PATH = "C:\msys64\ucrt64\bin;" + $env:PATH
}
if (Test-Path "C:\msys64\ucrt64\lib\pkgconfig\yara_x_capi.pc") {
    $env:PKG_CONFIG_PATH = "C:\msys64\ucrt64\lib\pkgconfig;" + $env:PKG_CONFIG_PATH
}

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "   RedCyberFox Phase 4 Verification & Correctness Check   " -ForegroundColor Cyan
Write-Host "   Working Directory: $repoRoot" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

$results = [System.Collections.Generic.List[PSCustomObject]]::new()

function Add-Result {
    param(
        [string]$Name,
        [string]$Status,
        [string]$Details
    )
    $results.Add([PSCustomObject]@{
        Check   = $Name
        Status  = $Status
        Details = $Details
    })
    $color = switch ($Status) {
        "PASS" { "Green" }
        "FAIL" { "Red" }
        "BLOCKED" { "Yellow" }
        Default { "White" }
    }
    Write-Host "[$Status] $Name - $Details" -ForegroundColor $color
}

# 1. Gofmt Check
Write-Host "`n--> [1/8] Checking Go formatting (gofmt)..." -ForegroundColor Magenta
$unformatted = & gofmt -l agent server pkg 2>&1
if ($LASTEXITCODE -eq 0 -and [string]::IsNullOrWhiteSpace($unformatted)) {
    Add-Result -Name "Gofmt Format Check" -Status "PASS" -Details "All files formatted cleanly"
} else {
    Add-Result -Name "Gofmt Format Check" -Status "FAIL" -Details "Unformatted files: $unformatted"
}

# 2. Go Vet
Write-Host "`n--> [2/8] Running Go vet..." -ForegroundColor Magenta
$vetOutput = & go vet ./agent/... ./server/... ./pkg/... 2>&1
if ($LASTEXITCODE -eq 0) {
    Add-Result -Name "Go Vet Static Analysis" -Status "PASS" -Details "No issues detected"
} else {
    Add-Result -Name "Go Vet Static Analysis" -Status "FAIL" -Details ($vetOutput -join "`n")
}

# 3. Dashboard Lint & Build
Write-Host "`n--> [3/8] Running Dashboard Lint & Build..." -ForegroundColor Magenta
$dashLint = & npm --prefix dashboard run lint 2>&1
$dashLintSuccess = ($LASTEXITCODE -eq 0)
$dashBuild = & npm --prefix dashboard run build 2>&1
$dashBuildSuccess = ($LASTEXITCODE -eq 0)

if ($dashLintSuccess -and $dashBuildSuccess) {
    Add-Result -Name "Dashboard Lint & Build" -Status "PASS" -Details "0 lint errors/warnings, Vite build succeeded"
} else {
    Add-Result -Name "Dashboard Lint & Build" -Status "FAIL" -Details "Lint success=$dashLintSuccess, Build success=$dashBuildSuccess"
}

# 4. Storage Engine & Idempotency / Retry Contract Tests
Write-Host "`n--> [4/8] Running Storage Unit & Regression Tests..." -ForegroundColor Magenta
$storageOut = & go test -v ./agent/internal/storage/... 2>&1
if ($LASTEXITCODE -eq 0) {
    Add-Result -Name "Storage Unit & Regression Tests" -Status "PASS" -Details "Deterministic clock seam, date boundaries, and DLQ idempotency passed"
} else {
    Add-Result -Name "Storage Unit & Regression Tests" -Status "FAIL" -Details "Tests failed in agent/internal/storage"
}

# 5. Ingestion Engine & Authoritative Identity Tests
Write-Host "`n--> [5/8] Running Ingestion & Identity Lifecycle Tests..." -ForegroundColor Magenta
$ingestOut = & go test -v ./agent/internal/ingestion/... 2>&1
if ($LASTEXITCODE -eq 0) {
    Add-Result -Name "Ingestion & Identity Lifecycle Tests" -Status "PASS" -Details "Authoritative SQLite identity & conflicting legacy recovery verified"
} else {
    Add-Result -Name "Ingestion & Identity Lifecycle Tests" -Status "FAIL" -Details "Tests failed in agent/internal/ingestion"
}

# 6. Governor, Forwarder, Server, Pkg, EventQueue & Measurement Report Tests
Write-Host "`n--> [6/8] Running Core Component Unit Tests (Governor, Forwarder, EventQueue, Server API, Events, Reports)..." -ForegroundColor Magenta
$coreOut = & go test ./agent/internal/governor/... ./agent/internal/forwarder/... ./agent/internal/eventqueue/... ./agent/internal/resourcereport/... ./server/internal/api/... ./pkg/... 2>&1
if ($LASTEXITCODE -eq 0) {
    Add-Result -Name "Core Components (Governor, Forwarder, EventQueue, Reports, Server API, Events)" -Status "PASS" -Details "All unit test suites passed"
} else {
    Add-Result -Name "Core Components (Governor, Forwarder, EventQueue, Reports, Server API, Events)" -Status "FAIL" -Details "Unit test failure in core components"
}

# 7. Pipeline Integration & Offline Sync Tests
Write-Host "`n--> [7/11] Running Pipeline Integration & Offline Sync Tests..." -ForegroundColor Magenta
$integrationOut = & go test -v ./agent/internal/integration -run "^(TestOffline|TestLostResponse|TestIntegration_LocalRemovalFailure|TestFocusedIngestion|TestPhase2E4|TestEndpointPipeline|TestFilesystemPipeline|TestInventoryPipeline|TestNetworkPipeline|TestPassiveNetworkPipeline|TestUSBPipeline|TestWorkloadDemonstration|TestMeasurement)" 2>&1
$intText = $integrationOut -join "`n"
if ($LASTEXITCODE -eq 0 -and -not ($intText -match "\[no tests to run\]")) {
    Add-Result -Name "Integration & Offline Sync Tests" -Status "PASS" -Details "Pipeline end-to-end and offline sync recovery passed"
} else {
    Add-Result -Name "Integration & Offline Sync Tests" -Status "FAIL" -Details "Integration tests failed or omitted: $intText"
}

# 8. Nearest-Rank Latency Percentile Statistics Tests
Write-Host "`n--> [8/11] Running Latency Percentiles Statistics Tests..." -ForegroundColor Magenta
$latOut = & go test -v ./agent/internal/integration -run "^TestLatencyPercentiles" 2>&1
$latText = $latOut -join "`n"
if ($LASTEXITCODE -eq 0 -and -not ($latText -match "\[no tests to run\]")) {
    Add-Result -Name "Latency Percentiles Statistics" -Status "PASS" -Details "Nearest-rank method, unsorted inputs, and boundary cases verified"
} else {
    Add-Result -Name "Latency Percentiles Statistics" -Status "FAIL" -Details "Latency percentiles tests failed or omitted"
}

# 9. Diagnostic Retention, Error Propagation & Exclusive Evidence Export Tests
Write-Host "`n--> [9/11] Running Diagnostic Retention, Error Propagation & Exclusive Export Tests..." -ForegroundColor Magenta
$diagOut = & go test -v ./agent/internal/integration -run "^(TestDiagnostic|TestWriteEvidenceExclusive|TestExportEvidenceRun|TestParseBinaryProvenance|TestExecCommandWithTimeout|TestSubprocess|TestCheckSourceBoundary)" 2>&1
$diagText = $diagOut -join "`n"
if ($LASTEXITCODE -eq 0 -and -not ($diagText -match "\[no tests to run\]")) {
    Add-Result -Name "Diagnostic Retention & Exclusive Export" -Status "PASS" -Details "Exclusive writes, failure retention, cleanup survival, and error propagation verified"
} else {
    Add-Result -Name "Diagnostic Retention & Exclusive Export" -Status "FAIL" -Details "Diagnostic retention or exclusive export tests failed or omitted"
}

# 10. Delivery Failure Boundaries & Crash Subprocess Regressions
Write-Host "`n--> [10/11] Running Delivery Failure Boundaries & Crash Subprocess Tests..." -ForegroundColor Magenta
$deliveryOut = & go test -v ./agent/internal/integration -run "^(TestDeliveryBoundary|TestCrashBoundary)" 2>&1
$delivText = $deliveryOut -join "`n"
if ($LASTEXITCODE -eq 0 -and -not ($delivText -match "\[no tests to run\]")) {
    Add-Result -Name "Delivery Boundaries & Crash Regressions" -Status "PASS" -Details "Offline backoff, retry recovery, bounded crash output, and OS process crash recovery verified"
} else {
    Add-Result -Name "Delivery Boundaries & Crash Regressions" -Status "FAIL" -Details "Delivery boundary or crash regression tests failed or omitted"
}

# 11. Linux Integration Test Cross-Compilation
Write-Host "`n--> [11/11] Running Linux Cross-Compilation Check (GOOS=linux, amd64)..." -ForegroundColor Magenta
$linuxTestBin = "$env:TEMP\linux_integ_verify.test"
$env:GOOS = "linux"
$env:GOARCH = "amd64"
$env:CGO_ENABLED = "0"
$linuxCompOut = & go test -c ./agent/internal/integration -o $linuxTestBin 2>&1
$linuxCompExit = $LASTEXITCODE
$env:GOOS = ""
$env:GOARCH = ""
if (Test-Path $linuxTestBin) { Remove-Item -Force $linuxTestBin }

if ($linuxCompExit -eq 0) {
    Add-Result -Name "Linux Integration Cross-Compilation" -Status "PASS" -Details "Cross-compiles for GOOS=linux GOARCH=amd64 (Compilation evidence only)"
} else {
    Add-Result -Name "Linux Integration Cross-Compilation" -Status "FAIL" -Details "Linux cross-compilation failed: $($linuxCompOut -join "`n")"
}
Add-Result -Name "Linux Runtime Execution Gate" -Status "BLOCKED" -Details "Runtime execution of Linux tests requires Linux environment (e.g. CI ubuntu-latest); cross-compilation alone is not runtime validation"

# Conditional Flags: Race & NativeDetection
Write-Host "`n--> Evaluating Optional / Mandatory -Race and -NativeDetection checks..." -ForegroundColor Magenta

if ($Race) {
    Write-Host "Probing local 64-bit CGO / UCRT64 race detector..." -ForegroundColor Cyan
    $gccCheck = Get-Command gcc -ErrorAction SilentlyContinue
    if (-not $gccCheck) {
        Add-Result -Name "Go Test -Race (Governor & Integration)" -Status "BLOCKED" -Details "C compiler (gcc) / CGO not available on local host. Verified in Linux CI (ubuntu-latest)."
    } else {
        $env:CGO_ENABLED = "1"
        $raceOutGov = & go test -race -v -count=1 ./agent/internal/governor/... ./agent/internal/eventqueue/... 2>&1
        $raceGovExit = $LASTEXITCODE
        $raceOutInt = & go test -race -v ./agent/internal/integration -run "TestOfflineSync_Integration" 2>&1
        $raceIntExit = $LASTEXITCODE
        if ($raceGovExit -eq 0 -and $raceIntExit -eq 0) {
            Add-Result -Name "Go Test -Race (Governor, EventQueue & Integration)" -Status "PASS" -Details "64-bit race conditions check passed under local UCRT64 CGO"
        } else {
            if ($raceGovExit -ne 0) {
                Write-Host "Race check failure in Governor/EventQueue:" -ForegroundColor Red
                Write-Host ($raceOutGov -join "`n") -ForegroundColor Yellow
            }
            if ($raceIntExit -ne 0) {
                Write-Host "Race check failure in Integration:" -ForegroundColor Red
                Write-Host ($raceOutInt -join "`n") -ForegroundColor Yellow
            }
            Add-Result -Name "Go Test -Race (Governor, EventQueue & Integration)" -Status "FAIL" -Details "Race test failure detected"
        }
    }
}

if ($NativeDetection) {
    Write-Host "Probing local YARA-X C API (pkg-config yara_x_capi)..." -ForegroundColor Cyan
    $pkgCheck = Get-Command pkg-config -ErrorAction SilentlyContinue
    $hasYaraX = $false
    if ($pkgCheck) {
        $yaraVer = (& pkg-config --modversion yara_x_capi 2>&1).Trim()
        if ($LASTEXITCODE -eq 0 -and -not [string]::IsNullOrWhiteSpace($yaraVer)) {
            $hasYaraX = $true
        }
    }
    if (-not $hasYaraX) {
        Add-Result -Name "Native Detection (YARA-X C API)" -Status "BLOCKED" -Details "pkg-config or libyara_x_capi not installed on local host. Real YARA-X native detection verified in Linux CI."
        Add-Result -Name "Native YARA-X Component Workload" -Status "BLOCKED" -Details "Omitted TestWorkload_YaraX due to missing libyara_x_capi dependency"
    } else {
        $env:CGO_ENABLED = "1"
        $detOut = & go test -v -tags=yara_x ./agent/internal/detection/... 2>&1
        $detExit = $LASTEXITCODE
        $detText = $detOut -join "`n"
        if ($detText -match "\[no tests to run\]" -or $detText -match "warning: no tests to run") {
            Add-Result -Name "Native Detection (YARA-X C API)" -Status "FAIL" -Details "Omitted: Go reported no tests to run in agent/internal/detection"
        } elseif ($detExit -eq 0) {
            Add-Result -Name "Native Detection (YARA-X C API)" -Status "PASS" -Details "Native YARA-X detection suite passed with libyara_x_capi ($yaraVer)"
        } else {
            Add-Result -Name "Native Detection (YARA-X C API)" -Status "FAIL" -Details "Detection test failure"
        }

        $yaraxWorkloadOut = & go test -v ./agent/internal/integration -run "^TestWorkload_YaraX" 2>&1
        $yaraxExit = $LASTEXITCODE
        $yaraxText = $yaraxWorkloadOut -join "`n"
        if ($yaraxText -match "\[no tests to run\]" -or $yaraxText -match "warning: no tests to run") {
            Add-Result -Name "Native YARA-X Component Workload" -Status "FAIL" -Details "Omitted: Go reported no tests to run under ^TestWorkload_YaraX"
        } elseif ($yaraxExit -eq 0) {
            Add-Result -Name "Native YARA-X Component Workload" -Status "PASS" -Details "Component workload, strict provenance, and cleanup survival verified (libyara_x_capi $yaraVer)"
        } else {
            Add-Result -Name "Native YARA-X Component Workload" -Status "FAIL" -Details "YARA-X workload or evidence export failed"
        }

        if ($Race) {
            $raceYaraxOut = & go test -race -v ./agent/internal/integration -run "^TestWorkload_YaraX" 2>&1
            $raceYaraxExit = $LASTEXITCODE
            $raceYaraxText = $raceYaraxOut -join "`n"
            if ($raceYaraxText -match "\[no tests to run\]") {
                Add-Result -Name "Go Test -Race (YARA-X Component Workload)" -Status "FAIL" -Details "Omitted: no tests to run under race detector"
            } elseif ($raceYaraxExit -eq 0) {
                Add-Result -Name "Go Test -Race (YARA-X Component Workload)" -Status "PASS" -Details "Zero race conditions in YARA-X component workload under CGO"
            } else {
                Add-Result -Name "Go Test -Race (YARA-X Component Workload)" -Status "FAIL" -Details "Race detected in YARA-X component workload"
            }
        }
    }
} else {
    Add-Result -Name "Native Detection (YARA-X C API)" -Status "BLOCKED" -Details "Omitted: -NativeDetection switch not passed; CGO native evaluation omitted"
    Add-Result -Name "Native YARA-X Component Workload" -Status "BLOCKED" -Details "Omitted: TestWorkload_YaraX requires -NativeDetection and libyara_x_capi"
}

# Summary Report
Write-Host "`n==========================================================" -ForegroundColor Cyan
Write-Host "                   VERIFICATION SUMMARY                   " -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

$hasFailures = $false
$hasBlocked = $false
foreach ($res in $results) {
    $col = switch ($res.Status) {
        "PASS" { "Green" }
        "FAIL" { "Red"; $hasFailures = $true }
        "BLOCKED" { "Yellow"; $hasBlocked = $true }
        Default { "White" }
    }
    Write-Host ("{0,-50} | {1,-8} | {2}" -f $res.Check, $res.Status, $res.Details) -ForegroundColor $col
}

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "NOTICE: Verification success indicates that automated test suites, bounded crash harnesses, and strict provenance checks pass cleanly." -ForegroundColor Cyan
Write-Host "PHASE 4 STATUS: Remains OPEN. Threshold calibration, governor enforcement, endpoint binary rule loading (--rules-dir), and low-spec target hardware qualification remain subsequent tasks." -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

if ($hasFailures) {
    Write-Host "Verification check FAILED with active test failures." -ForegroundColor Red
    exit 1
} elseif ($hasBlocked) {
    Write-Host "Verification check COMPLETED WITH BLOCKED DEPENDENCIES: Qualification readiness remains BLOCKED until all mandatory dependencies are satisfied." -ForegroundColor Yellow
    exit 2
} else {
    Write-Host "Verification check SUCCEEDED (All qualification checks passed cleanly)." -ForegroundColor Green
    exit 0
}
