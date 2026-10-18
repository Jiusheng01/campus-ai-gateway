param(
    [switch]$ForceRestart,
    [switch]$SkipTests,
    [switch]$NoBrowser,
    # Run the gateway's own data plane instead of ThinkWatch. Equivalent to
    # setting DATA_PLANE=direct, and it stops this script from starting twcore.
    [switch]$Direct,
    [string]$AdminToken = "campus-admin-local-secret",
    [string]$HmacSecret = "campus-local-test-secret"
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

function Import-UserEnv([string]$Name) {
    if (-not [Environment]::GetEnvironmentVariable($Name, "Process")) {
        $value = [Environment]::GetEnvironmentVariable($Name, "User")
        if (-not $value) {
            $value = [Environment]::GetEnvironmentVariable($Name, "Machine")
        }
        if ($value) {
            [Environment]::SetEnvironmentVariable($Name, $value, "Process")
        }
    }
}

function Find-TwCore {
    if ($env:TWCORE_EXE -and (Test-Path $env:TWCORE_EXE)) {
        return $env:TWCORE_EXE
    }

    $cmd = Get-Command twcore -ErrorAction SilentlyContinue
    if ($cmd -and $cmd.Source) {
        return $cmd.Source
    }

    $candidates = @()
    if ($env:LOCALAPPDATA) {
        $candidates += (Join-Path $env:LOCALAPPDATA "Programs\ThinkWatch Lite\twcore\twcore.exe")
        $candidates += (Join-Path $env:LOCALAPPDATA "Programs\ThinkWatch Lite\resources\twcore\twcore.exe")
        $candidates += (Join-Path $env:LOCALAPPDATA "ThinkWatch\twcore.exe")
    }
    if ($env:ProgramFiles) {
        $candidates += (Join-Path $env:ProgramFiles "ThinkWatch Lite\twcore\twcore.exe")
        $candidates += (Join-Path $env:ProgramFiles "ThinkWatch\twcore.exe")
    }

    foreach ($candidate in $candidates) {
        if ($candidate -and (Test-Path $candidate)) {
            return $candidate
        }
    }

    return $null
}

function Wait-TcpPort([string]$HostName, [int]$Port, [int]$Seconds = 30) {
    for ($i = 0; $i -lt $Seconds; $i++) {
        try {
            $client = New-Object System.Net.Sockets.TcpClient
            $task = $client.ConnectAsync($HostName, $Port)
            if ($task.Wait(700) -and $client.Connected) {
                $client.Close()
                return $true
            }
            $client.Close()
        }
        catch {
        }
        Start-Sleep -Seconds 1
    }
    return $false
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

Write-Host ""
Write-Host "============================================" -ForegroundColor DarkCyan
Write-Host " Campus AI Gateway - One Click Dev Startup" -ForegroundColor Cyan
Write-Host "============================================" -ForegroundColor DarkCyan
Write-Host ""

Refresh-Path
Import-UserEnv "TWCORE_EXE"
Import-UserEnv "THINKWATCH_GATEWAY_KEY"
Import-UserEnv "DASHSCOPE_API_KEY"

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Fail "Go not found. Install Go or add C:\Program Files\Go\bin to Windows PATH."
}
Write-Ok (& go version)

if (-not (Get-Command npm -ErrorAction SilentlyContinue)) {
    Fail "npm not found. Admin UI v2 requires Node.js 22+ at build time. Install Node.js LTS; Node is not required at Gateway runtime."
}
Write-Ok ("npm " + (& npm --version))

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Fail "Docker command not found. Install Docker Desktop first."
}

if (-not (Test-DockerReady)) {
    $dockerDesktop = "C:\Program Files\Docker\Docker\Docker Desktop.exe"
    if (Test-Path $dockerDesktop) {
        Write-Step "Docker Desktop is not ready. Starting it..."
        Start-Process $dockerDesktop | Out-Null
        $dockerReady = $false
        for ($i = 0; $i -lt 90; $i++) {
            if (Test-DockerReady) {
                $dockerReady = $true
                break
            }
            Start-Sleep -Seconds 1
        }
        if (-not $dockerReady) {
            Fail "Docker Desktop did not become ready within 90 seconds."
        }
    }
    else {
        Fail "Docker daemon is not running."
    }
}
Write-Ok "Docker is ready"

$composeFiles = @(
    "-f", ".\deploy\docker-compose.yml",
    "-f", ".\deploy\docker-compose.dev.yml"
)

Write-Step "Starting PostgreSQL and Redis..."
Invoke-Native { & docker compose @composeFiles up -d postgres redis }
if (-not (Test-NativeSuccess)) {
    Fail "Unable to start PostgreSQL / Redis."
}
Write-Ok "PostgreSQL / Redis containers started"

Write-Step "Waiting for PostgreSQL..."
$dbReady = $false
for ($i = 0; $i -lt 30; $i++) {
    Invoke-Native { & docker compose @composeFiles exec -T postgres pg_isready -U campus -d campus_gateway *> $null }
    if (Test-NativeSuccess) {
        $dbReady = $true
        break
    }
    Start-Sleep -Seconds 1
}
if (-not $dbReady) {
    Fail "PostgreSQL did not become ready."
}
Write-Ok "PostgreSQL is ready"

$env:DATABASE_URL = "postgres://campus:campus@127.0.0.1:5432/campus_gateway?sslmode=disable"

Write-Step "Applying database migrations..."
# The migrate command is the single source of truth for the schema. It applies
# every file in order, records what it applied in schema_migrations, and
# refuses to replay a historical file whose bytes changed — the per-file psql
# loop this replaces had no record at all, so a partially applied set was
# invisible until the gateway failed at runtime. It is the same binary the
# Docker stack runs as its startup gate.
Invoke-Native { & go run ./cmd/migrate -database-url $env:DATABASE_URL -migrations ".\migrations" }
if (-not (Test-NativeSuccess)) {
    Fail "Migration failed."
}
Write-Ok "Database migrations complete"

if (-not $env:ADMIN_TOKEN) {
    $env:ADMIN_TOKEN = $AdminToken
}
if (-not $env:API_KEY_HMAC_SECRET) {
    $env:API_KEY_HMAC_SECRET = $HmacSecret
}
$env:REDIS_ADDR = "127.0.0.1:6379"

# ThinkWatch is optional. The gateway runs the direct data plane when
# THINKWATCH_URL is empty, so an operator who does not want a second process
# sets DATA_PLANE=direct (or just leaves THINKWATCH_URL blank) and this script
# stops trying to start twcore. The default stays "use ThinkWatch if it is
# configured", which is what every existing local setup expects.
if (-not $env:DATA_PLANE) {
    $env:DATA_PLANE = "auto"
}
if ($Direct) {
    $env:DATA_PLANE = "direct"
}
if ($env:DATA_PLANE -eq "direct") {
    $env:THINKWATCH_URL = ""
}
elseif (-not $env:THINKWATCH_URL) {
    $env:THINKWATCH_URL = "http://127.0.0.1:8788"
}

$useThinkWatch = $env:DATA_PLANE -ne "direct" -and [bool]$env:THINKWATCH_URL
if ($useThinkWatch) {
    Write-Host ("       Data plane: ThinkWatch (" + $env:THINKWATCH_URL + ")")
}
else {
    Write-Host "       Data plane: direct (channels are dialled in-process)"
}

if (-not $env:THINKWATCH_CONFIG_PATH) {
    if ($env:APPDATA) {
        $env:THINKWATCH_CONFIG_PATH = Join-Path $env:APPDATA "ThinkWatch\config.yaml"
    }
}
if ($env:THINKWATCH_CONFIG_PATH) {
    Write-Host ("       ThinkWatch config: " + $env:THINKWATCH_CONFIG_PATH)
}

if (-not $env:TWCORE_EXE) {
    $detectedTwCore = Find-TwCore
    if ($detectedTwCore) {
        $env:TWCORE_EXE = $detectedTwCore
        Write-Ok ("Detected ThinkWatch Core: " + $env:TWCORE_EXE)
    }
}

$thinkWatchRunning = $false
if ($useThinkWatch) {
    $thinkWatchRunning = Wait-TcpPort "127.0.0.1" 8788 1
}
if ($useThinkWatch -and -not $thinkWatchRunning) {
    $twExe = $env:TWCORE_EXE
    if ($twExe -and (Test-Path $twExe)) {
        if (-not $env:DASHSCOPE_API_KEY) {
            Write-Warn "DASHSCOPE_API_KEY is not set. ThinkWatch may start, but Qwen calls can fail."
        }
        if ($env:THINKWATCH_CONFIG_PATH -and (Test-Path $env:THINKWATCH_CONFIG_PATH)) {
            Write-Step "Validating ThinkWatch config..."
            Invoke-Native { & $twExe --config $env:THINKWATCH_CONFIG_PATH check }
            if (-not (Test-NativeSuccess)) {
                Fail "ThinkWatch config validation failed. Core was not started."
            }
        }
        Write-Step "Starting ThinkWatch Core..."
        $psi = [System.Diagnostics.ProcessStartInfo]::new()
        $psi.FileName = $twExe
        $psi.WorkingDirectory = Split-Path $twExe -Parent
        $psi.UseShellExecute = $false
        $psi.CreateNoWindow = $true
        if ($env:THINKWATCH_CONFIG_PATH) {
            $psi.ArgumentList.Add("--config")
            $psi.ArgumentList.Add($env:THINKWATCH_CONFIG_PATH)
        }
        $psi.ArgumentList.Add("serve")
        try {
            $twProcess = [System.Diagnostics.Process]::Start($psi)
            Write-Host ("       ThinkWatch PID=" + $twProcess.Id)
        }
        catch {
            Fail ("Unable to start ThinkWatch Core: " + $_.Exception.Message)
        }
        $thinkWatchRunning = Wait-TcpPort "127.0.0.1" 8788 15
    }
}

if ($thinkWatchRunning) {
    Write-Ok "ThinkWatch is listening on :8788"
}
elseif ($useThinkWatch) {
    Write-Warn "ThinkWatch is not running on :8788 and twcore.exe could not be started automatically."
    Write-Warn "Set TWCORE_EXE once if automatic detection cannot find your installation."
}

if ($useThinkWatch -and -not $env:THINKWATCH_GATEWAY_KEY) {
    Write-Warn "THINKWATCH_GATEWAY_KEY is not set. The admin UI can still open, but real model proxy requests may fail."
}

Write-Step "Building Admin UI v2..."
$adminDir = Join-Path $RepoRoot "web\admin"
$adminPackage = Join-Path $adminDir "package.json"
$adminNodeModules = Join-Path $adminDir "node_modules"

if (-not (Test-Path $adminPackage)) {
    Fail ("Admin UI package.json not found: " + $adminPackage)
}

Push-Location $adminDir
try {
    if (-not (Test-Path $adminNodeModules)) {
        Invoke-Native { & npm install --no-audit --no-fund }
        if (-not (Test-NativeSuccess)) {
            Fail "Unable to install Admin UI dependencies."
        }
    }

    Invoke-Native { & npm run build }
    if (-not (Test-NativeSuccess)) {
        Fail "Admin UI build failed."
    }
}
finally {
    Pop-Location
}
Write-Ok "Admin UI v2 built"

if (-not $SkipTests) {
    Write-Step "Running Go tests..."
    Invoke-Native { & go test ./... }
    if (-not (Test-NativeSuccess)) {
        Fail "Go tests failed."
    }
    Write-Ok "Go tests passed"
}

$listeners = Get-NetTCPConnection -LocalPort 8080 -State Listen -ErrorAction SilentlyContinue
if ($listeners) {
    $pids = @($listeners | Select-Object -ExpandProperty OwningProcess -Unique)
    if ($ForceRestart) {
        foreach ($processId in $pids) {
            $process = Get-Process -Id $processId -ErrorAction SilentlyContinue
            if ($process) {
                Write-Step ("Stopping existing :8080 process " + $process.ProcessName + " PID=" + $processId)
                Stop-Process -Id $processId -Force
            }
        }
        Start-Sleep -Milliseconds 500
    }
    else {
        $details = @()
        foreach ($processId in $pids) {
            $process = Get-Process -Id $processId -ErrorAction SilentlyContinue
            if ($process) {
                $details += ($process.ProcessName + " PID=" + $processId)
            }
        }
        Fail ("Port 8080 is already in use: " + ($details -join ", ") + ". Re-run with -ForceRestart.")
    }
}

Write-Step "Starting Go Gateway..."
$gatewayCommand = "Set-Location '" + $RepoRoot.Replace("'", "''") + "'; go run ./cmd/server"
$gatewayProcess = Start-Process powershell -ArgumentList "-NoExit", "-Command", $gatewayCommand -PassThru
Write-Host ("       Gateway shell PID=" + $gatewayProcess.Id)

Write-Step "Waiting for Gateway health check..."
$gatewayReady = $false
for ($i = 0; $i -lt 30; $i++) {
    try {
        $response = Invoke-WebRequest "http://127.0.0.1:8080/healthz" -UseBasicParsing -TimeoutSec 1
        if ($response.StatusCode -eq 200) {
            $gatewayReady = $true
            break
        }
    }
    catch {
    }
    Start-Sleep -Seconds 1
}
if (-not $gatewayReady) {
    Fail "Gateway did not become healthy on :8080."
}
Write-Ok "Gateway is healthy on :8080"

$adminURL = "http://127.0.0.1:8080/admin-ui"
if (-not $NoBrowser) {
    Start-Process $adminURL
}

Write-Host ""
Write-Host "============================================" -ForegroundColor DarkCyan
Write-Host " READY" -ForegroundColor Green
Write-Host "============================================" -ForegroundColor DarkCyan
Write-Host (" Admin UI:    " + $adminURL)
Write-Host (" Admin Token: " + $env:ADMIN_TOKEN)
Write-Host (" ThinkWatch:  http://127.0.0.1:8788")
Write-Host ""
Write-Host " Stop local services with:"
Write-Host "   .\scripts\stop.ps1"
Write-Host ""
