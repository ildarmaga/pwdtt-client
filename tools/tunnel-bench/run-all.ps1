param(
    [string]$Config = ".\bench-config.json",
    [string]$Bench = ".\tunnel-bench.exe",
    [string]$OutDir = ".\reports"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $Config)) {
    throw "Config not found: $Config (copy bench-config.example.json to bench-config.json)"
}
if (-not (Test-Path -LiteralPath $Bench)) {
    throw "Benchmark executable not found: $Bench"
}

$settings = Get-Content -LiteralPath $Config -Raw | ConvertFrom-Json
if ([string]::IsNullOrWhiteSpace($settings.target)) { throw "target is required" }
if ([string]::IsNullOrWhiteSpace($settings.token)) { throw "token is required" }

New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
$env:WDTT_BENCH_TOKEN = $settings.token
$results = @()

foreach ($entry in $settings.protocols) {
    if ($entry.enabled -eq $false) { continue }
    $name = [string]$entry.name
    $proxy = [string]$entry.proxy
    if ([string]::IsNullOrWhiteSpace($name) -or [string]::IsNullOrWhiteSpace($proxy)) {
        throw "Every enabled protocol needs name and proxy"
    }
    $reportPath = Join-Path $OutDir ($name + ".json")
    Write-Host ("=== {0} via {1} ===" -f $name, (($proxy -replace '//[^/@]+@', '//***@')))
    & $Bench -mode run -protocol $name -proxy $proxy -target $settings.target `
        -mb $settings.megabytes -streams $settings.streams -pings $settings.pings `
        -timeout $settings.timeout -json $reportPath
    $exitCode = $LASTEXITCODE
    if (Test-Path -LiteralPath $reportPath) {
        $result = Get-Content -LiteralPath $reportPath -Raw | ConvertFrom-Json
        $result | Add-Member -NotePropertyName exit_code -NotePropertyValue $exitCode
        $results += $result
    } else {
        $results += [pscustomobject]@{ protocol = $name; error = "runner produced no report"; exit_code = $exitCode }
    }
}

$summaryPath = Join-Path $OutDir "summary.json"
$results | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $summaryPath -Encoding utf8
$results | Select-Object protocol, download_mbps, upload_mbps, ping_p95_ms, loaded_ping_p95_ms, failed_connections, error | Format-Table -AutoSize
Write-Host "Summary: $summaryPath"

if (($results | Where-Object { $_.exit_code -ne 0 }).Count -gt 0) { exit 1 }
