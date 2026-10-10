param(
    [switch]$ForceRestart,
    [switch]$SkipTests,
    [switch]$NoBrowser,
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
        & docker info *> $null
        return ($LASTEXITCODE -eq 0)
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
& docker compose @composeFiles up -d postgres redis
if ($LASTEXITCODE -ne 0) {
    Fail "Unable to start PostgreSQL / Redis."
}
Write-Ok "PostgreSQL / Redis containers started"

Write-Step "Waiting for PostgreSQL..."
$dbReady = $false
for ($i = 0; $i -lt 30; $i++) {
    & docker compose @composeFiles exec -T postgres pg_isready -U campus -d campus_gateway *> $null
    if ($LASTEXITCODE -eq 0) {
        $dbReady = $true
        break
    }
    Start-Sleep -Seconds 1
}
if (-not $dbReady) {
    Fail "PostgreSQL did not become ready."
}
Write-Ok "PostgreSQL is ready"

Write-Step "Applying database migrations..."
$migrations = Get-ChildItem ".\migrations\*.sql" -File | Sort-Object Name
foreach ($migration in $migrations) {
    Write-Host ("       -> " + $migration.Name)
    Get-Content $migration.FullName -Raw |
        & docker compose @composeFiles exec -T postgres psql -v ON_ERROR_STOP=1 -U campus -d campus_gateway
    if ($LASTEXITCODE -ne 0) {
        Fail ("Migration failed: " + $migration.Name)
    }
}
Write-Ok "Database migrations complete"

if (-not $env:ADMIN_TOKEN) {
    $env:ADMIN_TOKEN = $AdminToken
}
if (-not $env:API_KEY_HMAC_SECRET) {
    $env:API_KEY_HMAC_SECRET = $HmacSecret
}
$env:DATABASE_URL = "postgres://campus:campus@127.0.0.1:5432/campus_gateway?sslmode=disable"
$env:REDIS_ADDR = "127.0.0.1:6379"
$env:THINKWATCH_URL = "http://127.0.0.1:8788"

if (-not $env:THINKWATCH_CONFIG_PATH) {
    if ($env:APPDATA) {
        $env:THINKWATCH_CONFIG_PATH = Join-Path $env:APPDATA "ThinkWatch\config.yaml"
    }
}
if ($env:THINKWATCH_CONFIG_PATH) {
    Write-Host ("       ThinkWatch config: " + $env:THINKWATCH_CONFIG_PATH)
}

$thinkWatchRunning = Wait-TcpPort "127.0.0.1" 8788 1
if (-not $thinkWatchRunning) {
    $twExe = $env:TWCORE_EXE
    if ($twExe -and (Test-Path $twExe)) {
        if (-not $env:DASHSCOPE_API_KEY) {
            Write-Warn "DASHSCOPE_API_KEY is not set. ThinkWatch may start, but Qwen calls can fail."
        }
        Write-Step "Starting ThinkWatch Core from TWCORE_EXE..."
        Start-Process -FilePath $twExe -ArgumentList "serve" -WorkingDirectory (Split-Path $twExe -Parent) | Out-Null
        $thinkWatchRunning = Wait-TcpPort "127.0.0.1" 8788 15
    }
}

if ($thinkWatchRunning) {
    Write-Ok "ThinkWatch is listening on :8788"
}
else {
    Write-Warn "ThinkWatch is not running on :8788. Set TWCORE_EXE or start twcore.exe serve manually."
}

if (-not $env:THINKWATCH_GATEWAY_KEY) {
    Write-Warn "THINKWATCH_GATEWAY_KEY is not set. The admin UI can still open, but real model proxy requests may fail."
}

if (-not $SkipTests) {
    Write-Step "Running Go tests..."
    & go test ./...
    if ($LASTEXITCODE -ne 0) {
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
