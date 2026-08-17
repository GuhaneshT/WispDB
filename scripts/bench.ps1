param(
    [string]$Target = "all"
)

$ErrorActionPreference = "Stop"

# ============================================================
# WispDB Benchmark + Profiling
# ============================================================

$ProjectRoot = Split-Path -Parent $PSScriptRoot
Set-Location $ProjectRoot

$Timestamp = Get-Date -Format "yyyyMMdd-HHmmss"

$ResultsRoot = Join-Path $ProjectRoot "benchmarks"
$RunDir = Join-Path $ResultsRoot $Timestamp

New-Item -ItemType Directory -Force -Path $RunDir | Out-Null

# ============================================================
# Benchmark selection
# ============================================================

switch ($Target.ToLower()) {

    "all" {
        $BenchRegex = "Benchmark"
    }

    "wal" {
        $BenchRegex = "BenchmarkWAL"
    }

    "insert" {
        $BenchRegex = "BenchmarkInsert"
    }

    "get" {
        $BenchRegex = "BenchmarkGet"
    }

    "parallel" {
        $BenchRegex = "Benchmark.*Parallel"
    }

    "flush" {
        $BenchRegex = "BenchmarkFlush"
    }

    "compaction" {
        $BenchRegex = "BenchmarkCompaction"
    }

    default {
        Write-Host ""
        Write-Host "Unknown target: $Target" -ForegroundColor Red
        Write-Host ""
        Write-Host "Available targets:"
        Write-Host "  all"
        Write-Host "  wal"
        Write-Host "  insert"
        Write-Host "  get"
        Write-Host "  parallel"
        Write-Host "  flush"
        Write-Host "  compaction"
        exit 1
    }
}

Write-Host ""
Write-Host "==============================================" -ForegroundColor Cyan
Write-Host "              WispDB Benchmark" -ForegroundColor Cyan
Write-Host "==============================================" -ForegroundColor Cyan
Write-Host ""

Write-Host "Project : $ProjectRoot"
Write-Host "Target  : $Target"
Write-Host "Regex   : $BenchRegex"
Write-Host "Results : $RunDir"
Write-Host ""

# ============================================================
# 1. CORRECTNESS
# ============================================================

Write-Host "[1/5] Running tests..." -ForegroundColor Yellow
Write-Host ""

go test ./...

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Write-Host "TESTS FAILED." -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "Tests PASSED." -ForegroundColor Green
Write-Host ""

# ============================================================
# 2. BENCHMARK
# ============================================================

Write-Host "[2/5] Running benchmarks..." -ForegroundColor Yellow
Write-Host ""

$BenchmarkFile = Join-Path $RunDir "benchmark.txt"

$Output = & go test ./main `
    "-bench=$BenchRegex" `
    "-benchmem" `
    "-count=5" `
    "-benchtime=3s" `
    2>&1

$Output | Tee-Object -FilePath $BenchmarkFile

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Write-Host "BENCHMARK FAILED." -ForegroundColor Red
    exit 1
}

$BenchmarkLines = @(
    $Output | Where-Object {
        $_ -match "^Benchmark"
    }
)

if ($BenchmarkLines.Count -eq 0) {
    Write-Host ""
    Write-Host "ERROR: No benchmarks were executed." -ForegroundColor Red
    Write-Host ""
    Write-Host "Command used:"
    Write-Host "go test ./main -bench=$BenchRegex -benchmem -count=5 -benchtime=3s"
    exit 1
}

Write-Host ""
Write-Host "Benchmark completed." -ForegroundColor Green
Write-Host "Saved: $BenchmarkFile"
Write-Host ""

# ============================================================
# 3. CPU PROFILE
# ============================================================

Write-Host "[3/5] CPU profiling..." -ForegroundColor Yellow
Write-Host ""

$CPUProfile = Join-Path $RunDir "cpu.prof"
$CPUOutput = Join-Path $RunDir "cpu.txt"

$Output = & go test ./main `
    "-bench=$BenchRegex" `
    "-benchtime=15s" `
    "-cpuprofile=$CPUProfile" `
    2>&1

$Output | Tee-Object -FilePath $CPUOutput

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Write-Host "CPU PROFILING FAILED." -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "CPU profile:"
Write-Host "  $CPUProfile" -ForegroundColor Green
Write-Host ""

# ============================================================
# 4. MEMORY PROFILE
# ============================================================

Write-Host "[4/5] Memory profiling..." -ForegroundColor Yellow
Write-Host ""

$MemProfile = Join-Path $RunDir "memory.prof"
$MemOutput = Join-Path $RunDir "memory.txt"

$Output = & go test ./main `
    "-bench=$BenchRegex" `
    "-benchmem" `
    "-benchtime=15s" `
    "-memprofile=$MemProfile" `
    2>&1

$Output | Tee-Object -FilePath $MemOutput

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Write-Host "MEMORY PROFILING FAILED." -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "Memory profile:"
Write-Host "  $MemProfile" -ForegroundColor Green
Write-Host ""

# ============================================================
# 5. MUTEX + BLOCK PROFILE
# ============================================================

Write-Host "[5/5] Mutex and block profiling..." -ForegroundColor Yellow
Write-Host ""

$MutexProfile = Join-Path $RunDir "mutex.prof"
$BlockProfile = Join-Path $RunDir "block.prof"
$ConcurrencyOutput = Join-Path $RunDir "concurrency.txt"

$Output = & go test ./main `
    "-bench=$BenchRegex" `
    "-benchtime=15s" `
    "-mutexprofile=$MutexProfile" `
    "-blockprofile=$BlockProfile" `
    2>&1

$Output | Tee-Object -FilePath $ConcurrencyOutput

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Write-Host "CONCURRENCY PROFILING FAILED." -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "Mutex profile:"
Write-Host "  $MutexProfile" -ForegroundColor Green

Write-Host ""
Write-Host "Block profile:"
Write-Host "  $BlockProfile" -ForegroundColor Green

# ============================================================
# SUMMARY
# ============================================================

Write-Host ""
Write-Host "==============================================" -ForegroundColor Cyan
Write-Host "              COMPLETE" -ForegroundColor Cyan
Write-Host "==============================================" -ForegroundColor Cyan
Write-Host ""

Write-Host "Results:"
Write-Host "  $RunDir" -ForegroundColor Green

Write-Host ""
Write-Host "Files:"
Write-Host "  benchmark.txt"
Write-Host "  cpu.prof"
Write-Host "  cpu.txt"
Write-Host "  memory.prof"
Write-Host "  memory.txt"
Write-Host "  mutex.prof"
Write-Host "  block.prof"
Write-Host "  concurrency.txt"

Write-Host ""
Write-Host "PPROF:"
Write-Host ""
Write-Host "CPU:"
Write-Host "  go tool pprof -http=:8080 $CPUProfile"

Write-Host ""
Write-Host "Memory:"
Write-Host "  go tool pprof -http=:8081 $MemProfile"

Write-Host ""
Write-Host "Mutex:"
Write-Host "  go tool pprof -http=:8082 $MutexProfile"

Write-Host ""
Write-Host "Block:"
Write-Host "  go tool pprof -http=:8083 $BlockProfile"

Write-Host ""