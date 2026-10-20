$ErrorActionPreference = "SilentlyContinue"

# Compose interpolates every service, including the gateway that this script
# only queries, and the production compose file requires a value for
# API_KEY_HMAC_SECRET. Local development uses dev.ps1's accepted default.
if (-not $env:API_KEY_HMAC_SECRET) {
    $env:API_KEY_HMAC_SECRET = "campus-local-test-secret"
}

function Port-Status([int]$Port) {
    $listener = Get-NetTCPConnection -LocalPort $Port -State Listen
    if ($listener) {
        $pidValue = ($listener | Select-Object -First 1 -ExpandProperty OwningProcess)
        $proc = Get-Process -Id $pidValue
        return "UP (" + $proc.ProcessName + " PID=" + $pidValue + ")"
    }
    return "DOWN"
}

Write-Host ""
Write-Host "Campus AI Gateway - Local Status" -ForegroundColor Cyan
Write-Host ("Gateway    : " + (Port-Status 8080))

try {
    & docker compose -f .\deploy\docker-compose.yml -f .\deploy\docker-compose.dev.yml ps
}
catch {
    Write-Warning "Unable to query Docker Compose."
}
Write-Host ""
