param(
    [string]$TwCorePath = "",
    [string]$ThinkWatchConfigPath = "",
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

function Find-TwCore([string]$RequestedPath, [string]$RepoRoot) {
    if ($RequestedPath -and (Test-Path $RequestedPath)) {
        return (Resolve-Path $RequestedPath).Path
    }

    $configured = Get-EnvValue "TWCORE_EXE"
    if ($configured -and (Test-Path $configured)) {
        return (Resolve-Path $configured).Path
    }

    $cmd = Get-Command twcore -ErrorAction SilentlyContinue
    if ($cmd -and $cmd.Source -and (Test-Path $cmd.Source)) {
        return $cmd.Source
    }

    $repoParent = Split-Path $RepoRoot -Parent
    $candidates = @(
        (Join-Path $repoParent "ThinkWatch-Core\twcore.exe"),
        "D:\AI\ThinkWatch-Core\twcore.exe"
    )

    if ($env:LOCALAPPDATA) {
        $candidates += (Join-Path $env:LOCALAPPDATA "Programs\ThinkWatch Lite\twcore\twcore.exe")
        $candidates += (Join-Path $env:LOCALAPPDATA "Programs\ThinkWatch Lite\resources\twcore\twcore.exe")
        $candidates += (Join-Path $env:LOCALAPPDATA "ThinkWatch\twcore.exe")
    }
    if ($env:ProgramFiles) {
        $candidates += (Join-Path $env:ProgramFiles "ThinkWatch Lite\twcore\twcore.exe")
        $candidates += (Join-Path $env:ProgramFiles "ThinkWatch\twcore.exe")
    }

    foreach ($candidate in $candidates | Select-Object -Unique) {
        if ($candidate -and (Test-Path $candidate)) {
            return (Resolve-Path $candidate).Path
        }
    }

    return $null
}

function Get-ThinkWatchClientKey([string]$ConfigPath) {
    if (-not (Test-Path $ConfigPath)) {
        return $null
    }

    $inClients = $false
    foreach ($line in Get-Content $ConfigPath) {
        if ($line -match '^clients:\s*$') {
            $inClients = $true
            continue
        }

        if ($inClients -and $line -match '^[A-Za-z_][A-Za-z0-9_-]*:\s*') {
            break
        }

        if ($inClients -and $line -match '^\s+key:\s*["'']?(tw-[A-Za-z0-9_-]+)["'']?\s*(?:#.*)?$') {
            return $Matches[1]
        }
    }

    return $null
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

Write-Step "Locating ThinkWatch Core..."
$twExe = Find-TwCore $TwCorePath $RepoRoot
if (-not $twExe) {
    Fail "twcore.exe was not found. Re-run with -TwCorePath 'D:\path\to\twcore.exe'."
}
Set-UserEnv "TWCORE_EXE" $twExe
Write-Ok ("TWCORE_EXE = " + $twExe)

if (-not $ThinkWatchConfigPath) {
    $ThinkWatchConfigPath = Get-EnvValue "THINKWATCH_CONFIG_PATH"
}
if (-not $ThinkWatchConfigPath) {
    if (-not $env:APPDATA) {
        Fail "APPDATA is unavailable; provide -ThinkWatchConfigPath explicitly."
    }
    $ThinkWatchConfigPath = Join-Path $env:APPDATA "ThinkWatch\config.yaml"
}
$ThinkWatchConfigPath = [IO.Path]::GetFullPath($ThinkWatchConfigPath)
Set-UserEnv "THINKWATCH_CONFIG_PATH" $ThinkWatchConfigPath
Write-Ok ("THINKWATCH_CONFIG_PATH = " + $ThinkWatchConfigPath)

$configDir = Split-Path $ThinkWatchConfigPath -Parent
if (-not (Test-Path $configDir)) {
    New-Item -ItemType Directory -Path $configDir -Force | Out-Null
}

if (-not (Test-Path $ThinkWatchConfigPath)) {
    Write-Step "ThinkWatch config does not exist. Initializing..."
    & $twExe --config $ThinkWatchConfigPath init
    if ($LASTEXITCODE -ne 0) {
        Fail "twcore init failed."
    }
    Write-Ok "ThinkWatch config created"
}

Write-Step "Validating ThinkWatch config..."
& $twExe --config $ThinkWatchConfigPath check
if ($LASTEXITCODE -ne 0) {
    Fail "ThinkWatch config validation failed. Fix config.yaml before continuing."
}
Write-Ok "ThinkWatch config is valid"

$gatewayKey = Get-ThinkWatchClientKey $ThinkWatchConfigPath
if ($gatewayKey) {
    Set-UserEnv "THINKWATCH_GATEWAY_KEY" $gatewayKey
    Write-Ok "ThinkWatch gateway client key detected and saved to Windows User environment"
}
else {
    Write-Warn "No tw-* client key was found in ThinkWatch config. Real model proxy requests may fail."
}

$providerKeyChanged = $false
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
            $providerKeyChanged = $true
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
Write-Host (" ThinkWatch Core:   " + $twExe)
Write-Host (" ThinkWatch config: " + $ThinkWatchConfigPath)
Write-Host " Daily command:"
Write-Host "   .\scripts\dev.ps1"
Write-Host ""

if (-not $NoStart) {
    if ($providerKeyChanged) {
        $listeners = Get-NetTCPConnection -LocalPort 8788 -State Listen -ErrorAction SilentlyContinue
        if ($listeners) {
            Write-Step "Restarting ThinkWatch so the new provider credential is loaded..."
            $pids = @($listeners | Select-Object -ExpandProperty OwningProcess -Unique)
            foreach ($processId in $pids) {
                Stop-Process -Id $processId -Force -ErrorAction SilentlyContinue
            }
            Start-Sleep -Milliseconds 500
        }
    }

    Write-Step "Starting the development stack..."
    & (Join-Path $PSScriptRoot "dev.ps1") -ForceRestart
}
