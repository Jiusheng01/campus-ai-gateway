param(
    [switch]$SkipProviderKey,
    [switch]$NoStart
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

function Refresh-Path {
    $machine = [Environment]::GetEnvironmentVariable("Path", "Machine")
    $user = [Environment]::GetEnvironmentVariable("Path", "User")
    $env:Path = "$machine;$user"

    $defaultGo = "C:\Program Files\Go\bin"
    if (-not (Get-Command go -ErrorAction SilentlyContinue) -and (Test-Path "$defaultGo\go.exe")) {
        $env:Path += ";$defaultGo"
    }
}

function Get-EnvValue([string]$Name) {
    $value = [Environment]::GetEnvironmentVariable($Name, "Process")
    if (-not $value) {
        $value = [Environment]::GetEnvironmentVariable($Name, "User")
    }
    if (-not $value) {
        $value = [Environment]::GetEnvironmentVariable($Name, "Machine")
    }
    return $value
}

function Set-UserEnv([string]$Name, [string]$Value) {
    [Environment]::SetEnvironmentVariable($Name, $Value, "User")
    [Environment]::SetEnvironmentVariable($Name, $Value, "Process")
}

function Read-Secret([string]$Prompt) {
    $secure = Read-Host $Prompt -AsSecureString
    if (-not $secure -or $secure.Length -eq 0) {
        return ""
    }

    $ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try {
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr)
    }
    finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr)
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

$providerKey = Get-EnvValue "DASHSCOPE_API_KEY"
if ($providerKey) {
    [Environment]::SetEnvironmentVariable("DASHSCOPE_API_KEY", $providerKey, "Process")
    Write-Ok "DASHSCOPE_API_KEY is already configured"
}
elseif (-not $SkipProviderKey) {
    Write-Warn "DASHSCOPE_API_KEY is not configured."
    $answer = Read-Host "Save it to your Windows User environment now? [Y/n]"
    if ([string]::IsNullOrWhiteSpace($answer) -or $answer.Trim().ToLowerInvariant() -eq "y" -or $answer.Trim().ToLowerInvariant() -eq "yes") {
        $secret = Read-Secret "Enter DASHSCOPE_API_KEY (input is hidden)"
        if ($secret) {
            Set-UserEnv "DASHSCOPE_API_KEY" $secret
            $secret = $null
            Write-Ok "DASHSCOPE_API_KEY saved to Windows User environment"
        }
        else {
            Write-Warn "Empty key; skipped."
        }
    }
    else {
        Write-Warn "Skipped DASHSCOPE_API_KEY setup."
    }
}

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
