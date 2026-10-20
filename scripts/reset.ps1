param(
    [switch]$Force
)

$ErrorActionPreference = "Stop"

function Write-Step([string]$Message) {
    Write-Host ("[INFO] " + $Message) -ForegroundColor Cyan
}

function Write-Ok([string]$Message) {
    Write-Host ("[OK]   " + $Message) -ForegroundColor Green
}

function Write-Warn([string]$Message) {
    Write-Host ("[WARN] " + $Message) -ForegroundColor Yellow
}

function Fail([string]$Message) {
    Write-Host ("[FAIL] " + $Message) -ForegroundColor Red
    exit 1
}

# Windows PowerShell 5.1 turns anything a native executable writes to stderr
# into a terminating error while $ErrorActionPreference is "Stop" - even when
# the tool itself succeeded (docker compose progress, npm warnings, go test
# output). Invoke-Native scopes the preference down to "Continue" for the call,
# records the real exit code in $script:LastNativeExitCode, and restores the
# caller's preference afterwards so genuine PowerShell errors still terminate.
$script:LastNativeExitCode = 0

function Invoke-Native {
    param(
        [Parameter(Mandatory = $true)][scriptblock]$Command
    )
    $previous = $ErrorActionPreference
    $ErrorActionPreference = "Continue"
    try {
        & $Command
        $script:LastNativeExitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previous
    }
}

function Test-NativeSuccess {
    return ($script:LastNativeExitCode -eq 0)
}

function Refresh-Path {
    $machine = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $user = [Environment]::GetEnvironmentVariable("Path", "User")
    $env:Path = "$machine;$user"

    $defaultGo = "C:\Program Files\Go\bin"
    if (-not (Get-Command go -ErrorAction SilentlyContinue) -and (Test-Path "$defaultGo\go.exe")) {
        $env:Path += ";$defaultGo"
    }
}

function Test-DockerReady {
    try {
        Invoke-Native { & docker info *> $null }
        return (Test-NativeSuccess)
    }
    catch {
        return $false
    }
}

$RepoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $RepoRoot

# Compose interpolates every service, including the gateway that this script
# only tears down, and the production compose file requires a value for
# API_KEY_HMAC_SECRET. Local development uses dev.ps1's accepted default.
if (-not $env:API_KEY_HMAC_SECRET) {
    $env:API_KEY_HMAC_SECRET = "campus-local-test-secret"
}

Write-Host ""
Write-Host "============================================" -ForegroundColor DarkCyan
Write-Host " Campus AI Gateway - RESET LOCAL DATA" -ForegroundColor Red
Write-Host "============================================" -ForegroundColor DarkCyan
Write-Host ""
Write-Warn "This is destructive: local gateway data will be deleted, not archived."
Write-Host ""

Refresh-Path

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Fail "Docker command not found. Install Docker Desktop first."
}
if (-not (Test-DockerReady)) {
    Fail "Docker daemon is not running. Start Docker Desktop, then run this script again."
}
Write-Ok "Docker is ready"

# deploy/docker-compose.yml has no top-level "name:", so Compose derives the
# project name from the directory of the compose file: "deploy". Every checkout
# on this machine therefore shares the same project and the same named volumes
# unless COMPOSE_PROJECT_NAME (or -p) overrides it - which is exactly why a
# "new" clone can look like it already has channels and tenants. Honour the
# override here too, so reset targets the project this shell would otherwise
# start.
#
# Collision risk that the typed confirmation cannot remove: "down -v" deletes
# the volumes of the matched Compose project. If a second checkout of this repo
# uses the same project name (the default "deploy" does), both checkouts mount
# the same volumes, and resetting either one deletes the other's data. The
# project name below is normalized the way Compose normalizes it, printed, and
# passed with -p so the operator confirms the exact target rather than an
# assumed one.
$ProjectName = $env:COMPOSE_PROJECT_NAME
if ($ProjectName) {
    $ProjectName = $ProjectName.Trim().ToLowerInvariant()
}
if (-not $ProjectName) {
    $ProjectName = "deploy"
}
# Compose accepts only lowercase letters, digits, dashes and underscores, and
# requires the name to begin with a letter or digit. Refuse anything else rather
# than silently targeting a project different from the one Compose would use.
if ($ProjectName -notmatch '^[a-z0-9][a-z0-9_-]*$') {
    Fail ("COMPOSE_PROJECT_NAME normalizes to '" + $ProjectName + "', which is not a valid Compose project name.")
}
$PostgresVolume = $ProjectName + "_postgres_data"
$RedisVolume = $ProjectName + "_redis_data"

Write-Host "The following will be removed:"
Write-Host ("  - Compose project '" + $ProjectName + "' (normalized; default project name: deploy)")
Write-Host ("  - Docker volume " + $PostgresVolume + ": administrators, tenants, API keys,")
Write-Host "    channels, models, routes, audit and request records"
Write-Host ("  - Docker volume " + $RedisVolume + ": rate-limit and quota counters")
Write-Host ""
Write-Warn "Another checkout of this repo that shares the project name '" + $ProjectName + "' also shares these volumes; this reset deletes its data too."
Write-Host ""

if (-not $Force) {
    # Typing the project name, not a fixed word, makes it impossible to confirm
    # without having read which project is about to be destroyed.
    $answer = Read-Host ("Type the Compose project name '" + $ProjectName + "' to continue, or press Enter to abort")
    if ("$answer".Trim() -cne $ProjectName) {
        Write-Host "Aborted. Nothing was deleted." -ForegroundColor Yellow
        Write-Host ""
        exit 0
    }
}
else {
    Write-Warn ("-Force given: skipping the typed confirmation. The irreversible target is Compose project '" + $ProjectName + "' and its volumes " + $PostgresVolume + ", " + $RedisVolume + ".")
}

Write-Step ("Removing Compose project '" + $ProjectName + "' and its volumes (docker compose down -v)...")
Invoke-Native { & docker compose -p $ProjectName -f .\deploy\docker-compose.yml down -v }
if (-not (Test-NativeSuccess)) {
    Fail "docker compose down -v failed."
}
Write-Ok "Stack stopped and volumes removed"

# Start through the normal path rather than reimplementing it: dev.ps1 waits
# for PostgreSQL, runs cmd/migrate, builds the console and starts the gateway.
# Migrations recreate the schema on the now-empty volume; nothing is seeded.
#
# dev.ps1's Fail helper calls exit 1. When a script is invoked with &, that exit
# returns here with $LASTEXITCODE=1 rather than ending this script, so the result
# is checked. The old "$global:LASTEXITCODE = 0" before the call is gone: the
# successful "down -v" above already left 0, so it had nothing to reset.
Write-Step "Starting from an empty database via dev.ps1..."
& (Join-Path $PSScriptRoot "dev.ps1") -ForceRestart
if ($LASTEXITCODE -ne 0) {
    Fail "dev.ps1 did not complete successfully."
}

Write-Host ""
Write-Host "============================================" -ForegroundColor DarkCyan
Write-Host " RESET COMPLETE - DATABASE IS EMPTY" -ForegroundColor Green
Write-Host "============================================" -ForegroundColor DarkCyan
Write-Host " Create the first administrator at http://127.0.0.1:8080/admin-ui"
Write-Host ""
