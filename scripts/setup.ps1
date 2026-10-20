param(
    [switch]$NoStart
)

$ErrorActionPreference = "Stop"

function Write-Step([string]$Message) {
    Write-Host ("[INFO] " + $Message) -ForegroundColor Cyan
}

function Write-Ok([string]$Message) {
    Write-Host ("[OK]   " + $Message) -ForegroundColor Green
}

function Fail([string]$Message) {
    Write-Host ("[FAIL] " + $Message) -ForegroundColor Red
    exit 1
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

$RepoRoot = Split-Path -Parent $PSScriptRoot
Set-Location $RepoRoot

Write-Host ""
Write-Host "============================================" -ForegroundColor DarkCyan
Write-Host " Campus AI Gateway - First Time Setup" -ForegroundColor Cyan
Write-Host "============================================" -ForegroundColor DarkCyan
Write-Host ""

Refresh-Path

Write-Step "Checking Go..."
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Fail "Go not found. Install Go first, then run this script again."
}
Write-Ok (& go version)

Write-Step "Checking Docker..."
if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Fail "Docker command not found. Install Docker Desktop first, then run this script again."
}
Write-Ok "Docker command found"

Write-Host ""
Write-Host "============================================" -ForegroundColor DarkCyan
Write-Host " SETUP COMPLETE" -ForegroundColor Green
Write-Host "============================================" -ForegroundColor DarkCyan
Write-Host " Daily command:"
Write-Host "   .\scripts\dev.ps1"
Write-Host ""

if (-not $NoStart) {
    Write-Step "Starting the development stack..."
    & (Join-Path $PSScriptRoot "dev.ps1") -ForceRestart
}
