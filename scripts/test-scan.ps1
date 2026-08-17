# Test script for WispDB Scan feature
# Run all scan tests and show results

Write-Host "=== WispDB Scan Feature Tests ===" -ForegroundColor Cyan
Write-Host ""

# Run scan unit tests
Write-Host "Running Scan unit tests..." -ForegroundColor Yellow
go test ./main -run TestScan -v

Write-Host ""
Write-Host "Scan tests completed." -ForegroundColor Green
Write-Host ""

# Run scan benchmark
Write-Host "Running Scan benchmark..." -ForegroundColor Yellow
go test ./main -run BenchmarkScan -bench BenchmarkScan -benchmem

Write-Host ""
Write-Host "=== All Scan tests finished ===" -ForegroundColor Cyan
