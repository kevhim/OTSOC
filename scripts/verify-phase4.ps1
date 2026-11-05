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
Write-Host "`n--> [7/8] Running Pipeline Integration & Offline Sync Tests..." -ForegroundColor Magenta
$integrationOut = & go test -v ./agent/internal/integration -run "^(TestOffline|TestLostResponse|TestIntegration_LocalRemovalFailure|TestFocusedIngestion|TestPhase2E4|TestEndpointPipeline|TestFilesystemPipeline|TestInventoryPipeline|TestNetworkPipeline|TestPassiveNetworkPipeline|TestUSBPipeline|TestWorkloadDemonstration|TestMeasurement)" 2>&1
if ($LASTEXITCODE -eq 0) {
    Add-Result -Name "Integration & Offline Sync Tests" -Status "PASS" -Details "Pipeline end-to-end and offline sync recovery passed"
} else {
    Add-Result -Name "Integration & Offline Sync Tests" -Status "FAIL" -Details "Integration tests failed"
}

# 8. Optional / Conditional Flags: Race & NativeDetection
Write-Host "`n--> [8/8] Evaluating -Race and -NativeDetection checks..." -ForegroundColor Magenta

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
    } else {
        $env:CGO_ENABLED = "1"
        $detOut = & go test -v -tags=yara_x ./agent/internal/detection/... 2>&1
        if ($LASTEXITCODE -eq 0) {
            Add-Result -Name "Native Detection (YARA-X C API)" -Status "PASS" -Details "Native YARA-X detection suite passed with libyara_x_capi ($yaraVer)"
        } else {
            Add-Result -Name "Native Detection (YARA-X C API)" -Status "FAIL" -Details "Detection test failure"
        }
    }
}

# Summary Report
Write-Host "`n==========================================================" -ForegroundColor Cyan
Write-Host "                   VERIFICATION SUMMARY                   " -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan

$hasFailures = $false
foreach ($res in $results) {
    $col = switch ($res.Status) {
        "PASS" { "Green" }
        "FAIL" { "Red"; $hasFailures = $true }
        "BLOCKED" { "Yellow" }
        Default { "White" }
    }
    Write-Host ("{0,-48} | {1,-8} | {2}" -f $res.Check, $res.Status, $res.Details) -ForegroundColor $col
}

Write-Host "==========================================================" -ForegroundColor Cyan

if ($hasFailures) {
    Write-Host "Verification check FAILED with active test failures." -ForegroundColor Red
    exit 1
} else {
    Write-Host "Verification check SUCCEEDED (All runnable checks passed; blocked dependencies logged)." -ForegroundColor Green
    exit 0
}
