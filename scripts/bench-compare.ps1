#!/usr/bin/env pwsh
<#
.SYNOPSIS
Run tests and benchmarks, compare to previous baseline, report deltas.

.DESCRIPTION
1. Runs `go test ./...` for correctness gate
2. Runs `go test ./... -bench=. -benchmem` to collect benchmarks
3. Parses output, compares against baseline, prints delta table
4. Saves baseline for next run

Baseline stored at `.claude/benchmarks/baseline.json`
#>

$ErrorActionPreference = "Stop"

$projectRoot = Split-Path -Parent $PSScriptRoot
$baselinePath = Join-Path $projectRoot ".claude" "benchmarks" "baseline.json"
$baselineDir = Split-Path -Path $baselinePath

# Check Go is available
$goCmd = Get-Command go -ErrorAction SilentlyContinue
if (-not $goCmd) {
    Write-Host "❌ Go not found on PATH" -ForegroundColor Red
    Write-Host "Please install Go or add it to your PATH."
    exit 1
}

Write-Host "Running tests..." -ForegroundColor Cyan
$testOutput = & go test ./... 2>&1
$testSuccess = $LASTEXITCODE -eq 0

if ($testSuccess) {
    Write-Host "✓ All tests passed" -ForegroundColor Green
} else {
    Write-Host "❌ Tests failed:" -ForegroundColor Red
    Write-Host $testOutput
    exit 1
}

Write-Host "`nRunning benchmarks..." -ForegroundColor Cyan
$benchOutput = & go test ./... -bench=. -benchmem -run=^$ 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host "❌ Benchmark run failed:" -ForegroundColor Red
    Write-Host $benchOutput
    exit 1
}

# Parse benchmark output: Benchmark<name>-<num> <iterations> <ns/op> ns/op <bytes/op> B/op <allocs/op> allocs/op
$benchPattern = "^Benchmark(\S+)-\d+\s+\d+\s+([\d.]+)\s+ns/op\s+([\d.]+)\s+B/op\s+(\d+)\s+allocs/op"
$currentBenches = @{}

$benchOutput | ForEach-Object {
    if ($_ -match $benchPattern) {
        $name = $matches[1]
        $nsOp = [double]$matches[2]
        $bytesOp = [double]$matches[3]
        $allocsOp = [int]$matches[4]
        $currentBenches[$name] = @{
            ns = $nsOp
            bytes = $bytesOp
            allocs = $allocsOp
        }
    }
}

if ($currentBenches.Count -eq 0) {
    Write-Host "⚠ No benchmarks found in output" -ForegroundColor Yellow
    Write-Host $benchOutput
    exit 1
}

# Load baseline if it exists
$baseline = @{}
$isFirstRun = $false

if (Test-Path $baselinePath) {
    try {
        $baseline = Get-Content $baselinePath -Raw | ConvertFrom-Json -AsHashtable
    } catch {
        Write-Host "⚠ Failed to parse baseline file (corrupted?). Starting fresh." -ForegroundColor Yellow
        $baseline = @{}
        $isFirstRun = $true
    }
} else {
    $isFirstRun = $true
}

# Compute deltas and print report
Write-Host "`nBenchmark Results Comparison" -ForegroundColor Cyan
Write-Host "============================" -ForegroundColor Cyan

if ($isFirstRun) {
    Write-Host "`n📊 First benchmark run — establishing baseline" -ForegroundColor Green
    Write-Host "   (No prior results to compare against)`n" -ForegroundColor Gray
    foreach ($name in $currentBenches.Keys | Sort-Object) {
        $bench = $currentBenches[$name]
        Write-Host "  $name" -ForegroundColor Gray
        Write-Host "    ns/op: $($bench.ns)" -ForegroundColor Gray
        Write-Host "    B/op:  $($bench.bytes)" -ForegroundColor Gray
        Write-Host "    allocs: $($bench.allocs)" -ForegroundColor Gray
    }
} else {
    # Check for removed benchmarks
    $removed = @()
    foreach ($name in $baseline.Keys) {
        if (-not $currentBenches.ContainsKey($name)) {
            $removed += $name
        }
    }

    if ($removed.Count -gt 0) {
        Write-Host "`n⚠ Benchmarks removed:" -ForegroundColor Yellow
        foreach ($name in $removed | Sort-Object) {
            Write-Host "  - $name" -ForegroundColor Yellow
        }
        Write-Host ""
    }

    foreach ($name in $currentBenches.Keys | Sort-Object) {
        $current = $currentBenches[$name]
        $prior = $baseline[$name]

        if (-not $prior) {
            Write-Host "  $name (new)" -ForegroundColor Cyan
            Write-Host "    ns/op: $($current.ns) (new)" -ForegroundColor Cyan
            Write-Host "    B/op:  $($current.bytes) (new)" -ForegroundColor Cyan
            Write-Host "    allocs: $($current.allocs) (new)" -ForegroundColor Cyan
            continue
        }

        $nsDelta = $current.ns - $prior.ns
        $nsDeltaPct = if ($prior.ns -gt 0) { ($nsDelta / $prior.ns) * 100 } else { 0 }

        $bytesDelta = $current.bytes - $prior.bytes
        $bytesDeltaPct = if ($prior.bytes -gt 0) { ($bytesDelta / $prior.bytes) * 100 } else { 0 }

        $allocsDelta = $current.allocs - $prior.allocs
        $allocsDeltaPct = if ($prior.allocs -gt 0) { ($allocsDelta / $prior.allocs) * 100 } else { 0 }

        # Color: green for improvement (negative), red for regression (positive)
        $nsColor = if ($nsDelta -lt 0) { "Green" } elseif ($nsDelta -gt 0) { "Red" } else { "Gray" }
        $bytesColor = if ($bytesDelta -lt 0) { "Green" } elseif ($bytesDelta -gt 0) { "Red" } else { "Gray" }
        $allocsColor = if ($allocsDelta -lt 0) { "Green" } elseif ($allocsDelta -gt 0) { "Red" } else { "Gray" }

        Write-Host "  $name" -ForegroundColor Gray
        Write-Host "    ns/op:  $($prior.ns) → $($current.ns) ($([Math]::Round($nsDeltaPct, 1))%)" -ForegroundColor $nsColor
        Write-Host "    B/op:   $($prior.bytes) → $($current.bytes) ($([Math]::Round($bytesDeltaPct, 1))%)" -ForegroundColor $bytesColor
        Write-Host "    allocs: $($prior.allocs) → $($current.allocs) ($([Math]::Round($allocsDeltaPct, 1))%)" -ForegroundColor $allocsColor
    }
}

# Save baseline
if (-not (Test-Path $baselineDir)) {
    New-Item -ItemType Directory -Path $baselineDir -Force | Out-Null
}

$jsonBaseline = ConvertTo-Json $currentBenches
Set-Content -Path $baselinePath -Value $jsonBaseline

Write-Host "`n✓ Baseline updated: $baselinePath" -ForegroundColor Green
