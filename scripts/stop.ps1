param(
    [switch]$KeepDataServices
)

$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $RepoRoot

# Compose interpolates every service, including the gateway that this script
# only stops, and the production compose file requires a value for
# API_KEY_HMAC_SECRET. Local development uses dev.ps1's accepted default.
if (-not $env:API_KEY_HMAC_SECRET) {
    $env:API_KEY_HMAC_SECRET = "campus-local-test-secret"
}

Write-Host ""
Write-Host "Campus AI Gateway - Stop Local Dev" -ForegroundColor Cyan

$listeners = Get-NetTCPConnection -LocalPort 8080 -State Listen -ErrorAction SilentlyContinue
if ($listeners) {
    $pids = @($listeners | Select-Object -ExpandProperty OwningProcess -Unique)
    foreach ($processId in $pids) {
        $process = Get-Process -Id $processId -ErrorAction SilentlyContinue
        if ($process) {
            Write-Host ("Stopping Gateway process " + $process.ProcessName + " PID=" + $processId)
            Stop-Process -Id $processId -Force
        }
    }
}
else {
    Write-Host "No process is listening on :8080."
}

if (-not $KeepDataServices) {
    Write-Host "Stopping PostgreSQL and Redis containers..."
    & docker compose -f .\deploy\docker-compose.yml -f .\deploy\docker-compose.dev.yml stop postgres redis
    if ($LASTEXITCODE -ne 0) {
        Write-Warning "docker compose stop returned a non-zero exit code."
    }
}
else {
    Write-Host "Keeping PostgreSQL and Redis running."
}

Write-Host ""
Write-Host "Stopped." -ForegroundColor Green
Write-Host ""
