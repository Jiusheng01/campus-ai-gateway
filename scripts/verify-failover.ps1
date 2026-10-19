param(
    [string]$GatewayUrl = "http://127.0.0.1:8080",
    [string]$AdminToken = $env:ADMIN_TOKEN,
    [string]$APIKey = $env:CAMPUS_API_KEY,
    [string]$Model = "campus-smart",
    [string]$PrimaryProvider = "qwen",
    [string]$FallbackProvider = "deepseek",
    [switch]$Execute
)

$ErrorActionPreference = "Stop"

if (-not $Execute) {
    throw "This script temporarily changes the primary provider URL. Re-run with -Execute after reviewing the target environment."
}
if ([string]::IsNullOrWhiteSpace($AdminToken)) { throw "ADMIN_TOKEN is required." }
if ([string]::IsNullOrWhiteSpace($APIKey)) { throw "CAMPUS_API_KEY or -APIKey is required." }

$GatewayUrl = $GatewayUrl.TrimEnd("/")
$adminHeaders = @{ Authorization = "Bearer $AdminToken" }

function Invoke-AdminJson {
    param(
        [Parameter(Mandatory=$true)][string]$Path,
        [string]$Method = "GET",
        $Body = $null
    )
    $params = @{
        Uri = "$GatewayUrl$Path"
        Method = $Method
        Headers = $adminHeaders
    }
    if ($null -ne $Body) {
        $params.ContentType = "application/json"
        $params.Body = ($Body | ConvertTo-Json -Depth 10)
    }
    Invoke-RestMethod @params
}

$providers = @((Invoke-AdminJson -Path "/admin/providers").data)
$models = @((Invoke-AdminJson -Path "/admin/models").data)

$primary = $providers | Where-Object { $_.name -eq $PrimaryProvider } | Select-Object -First 1
$fallback = $providers | Where-Object { $_.name -eq $FallbackProvider } | Select-Object -First 1
$modelConfig = $models | Where-Object { $_.alias -eq $Model } | Select-Object -First 1

if ($null -eq $primary) { throw "Primary provider '$PrimaryProvider' was not found." }
if ($null -eq $fallback) { throw "Fallback provider '$FallbackProvider' was not found." }
if ($null -eq $modelConfig) { throw "Model '$Model' was not found." }

$routes = @((Invoke-AdminJson -Path "/admin/models/$($modelConfig.id)/routes").data)
$enabledRoutes = @($routes | Where-Object { $_.status -eq 1 } | Sort-Object priority, @{Expression="weight";Descending=$true})

if ($enabledRoutes.Count -lt 2) { throw "Model '$Model' must have at least two enabled routes." }
if ($enabledRoutes[0].provider_name -ne $PrimaryProvider) {
    throw "The first enabled route is '$($enabledRoutes[0].provider_name)', expected '$PrimaryProvider'."
}
if (-not ($enabledRoutes | Where-Object { $_.provider_name -eq $FallbackProvider })) {
    throw "No enabled fallback route for '$FallbackProvider'."
}

$original = @{
    name = $primary.name
    base_url = $primary.base_url
    protocol = $primary.protocol
    credential_env = $primary.credential_env
    status = $primary.status
}
$broken = @{
    name = $primary.name
    base_url = "http://127.0.0.1:1"
    protocol = $primary.protocol
    credential_env = $primary.credential_env
    status = $primary.status
}

$restored = $false
try {
    Write-Host "[1/3] Breaking primary provider '$PrimaryProvider' temporarily..."
    Invoke-AdminJson -Path "/admin/providers/$($primary.id)" -Method "PUT" -Body $broken | Out-Null
    # The direct data plane rebuilds its route table from PostgreSQL and caches
    # it for 5 seconds, so wait past that window before testing failover.
    Start-Sleep -Seconds 6

    Write-Host "[2/3] Sending request through virtual model '$Model'..."
    $payload = @{
        model = $Model
        messages = @(@{ role = "user"; content = "Reply with exactly FAILOVER_OK" })
        stream = $false
    } | ConvertTo-Json -Depth 10

    $requestParams = @{
        Uri = "$GatewayUrl/v1/chat/completions"
        Method = "POST"
        Headers = @{ Authorization = "Bearer $APIKey" }
        ContentType = "application/json"
        Body = $payload
    }
    $response = Invoke-RestMethod @requestParams

    $content = [string]$response.choices[0].message.content
    if ([string]::IsNullOrWhiteSpace($content)) {
        throw "The failover request returned no assistant content."
    }
    Write-Host "[PASS] Primary was unreachable and the virtual-model request still succeeded."
    Write-Host "       Response: $content"
}
finally {
    Write-Host "[3/3] Restoring primary provider..."
    try {
        Invoke-AdminJson -Path "/admin/providers/$($primary.id)" -Method "PUT" -Body $original | Out-Null
        $restored = $true
        Write-Host "[OK] Primary provider restored."
    }
    catch {
        Write-Error "RESTORE FAILED. Restore '$PrimaryProvider' manually before further testing. $($_.Exception.Message)"
    }
}

if (-not $restored) { exit 2 }
