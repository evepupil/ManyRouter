param(
    [switch]$BuildLocal,
    [switch]$SkipSmoke,
    [string]$EnvironmentFile = ""
)

if ($EnvironmentFile) { $env:MANYROUTER_RELEASE_ENV_FILE = $EnvironmentFile }
. (Join-Path $PSScriptRoot "common.ps1")

if (-not (Test-Path -LiteralPath $script:ReleaseEnvPath)) {
    Copy-Item -LiteralPath (Join-Path $script:ReleaseDeployDir ".env.example") -Destination $script:ReleaseEnvPath
    $generated = @{
        MANYROUTER_DB_PASSWORD = New-HexSecret
        MANYROUTER_MASTER_KEY = New-Base64Secret
        MANYROUTER_OPERATOR_TOKEN = New-URLSecret
        MANYROUTER_OWNER_PASSWORD = New-URLSecret -Bytes 24
        NEW_API_DB_ADMIN_PASSWORD = New-HexSecret
        NEW_API_DB_PASSWORD = New-HexSecret
        NEW_API_LOG_DB_PASSWORD = New-HexSecret
        NEW_API_SESSION_SECRET = New-URLSecret
        NEW_API_OWNER_PASSWORD = New-URLSecret -Bytes 24
        MANYROUTER_SYNC_TOKEN = New-URLSecret
    }
    foreach ($entry in $generated.GetEnumerator()) {
        Set-ReleaseEnvironmentValue -Key $entry.Key -Value $entry.Value
    }
    try {
        & icacls.exe $script:ReleaseEnvPath /inheritance:r /grant:r "$env:USERNAME`:(R,W)" | Out-Null
    } catch {
        Write-Warning "无法收紧 deploy/.env 权限，请手动限制该文件的读取范围。"
    }
}

if ($BuildLocal) {
    & (Join-Path $PSScriptRoot "preflight.ps1") -AllowMissingLocalImages | Out-Null
    $manyRouterCommit = (& git -C $script:ReleaseRepoRoot rev-parse --short=8 HEAD).Trim()
    $newAPIPath = Join-Path $script:ReleaseRepoRoot "new-api"
    $newAPICommit = (& git -C $newAPIPath rev-parse --short=8 HEAD).Trim()
    if ($LASTEXITCODE -ne 0) { throw "无法读取本地提交" }
    $manyRouterImage = "manyrouter-control:$manyRouterCommit"
    $newAPIImage = "manyrouter-new-api:$newAPICommit"
    Invoke-ReleaseDocker build --build-arg "BUILD_VERSION=m4-$manyRouterCommit" --build-arg "BUILD_COMMIT=$manyRouterCommit" --build-arg "GO_PROXY=https://goproxy.cn,direct" -t $manyRouterImage .
    Invoke-ReleaseDocker build --build-arg "GO_PROXY=https://goproxy.cn,direct" -t $newAPIImage ./new-api
    Set-ReleaseEnvironmentValue -Key "MANYROUTER_IMAGE" -Value $manyRouterImage
    Set-ReleaseEnvironmentValue -Key "MANYROUTER_BUILD_VERSION" -Value "m4-$manyRouterCommit"
    Set-ReleaseEnvironmentValue -Key "MANYROUTER_BUILD_COMMIT" -Value $manyRouterCommit
    Set-ReleaseEnvironmentValue -Key "NEW_API_IMAGE" -Value $newAPIImage
    Set-ReleaseEnvironmentValue -Key "NEW_API_BUILD_VERSION" -Value $newAPICommit
}

& (Join-Path $PSScriptRoot "preflight.ps1") | Out-Null
$environment = Get-ReleaseEnvironment
$manyRouterPort = [int]$environment["MANYROUTER_PORT"]
$newAPIPort = [int]$environment["NEW_API_PORT"]
$manyRouterURL = "http://127.0.0.1:$manyRouterPort"
$newAPIURL = "http://127.0.0.1:$newAPIPort"

Invoke-ReleaseCompose up -d
Wait-ReleaseEndpoint -Uri "$manyRouterURL/api/v1/healthz"
Wait-ReleaseEndpoint -Uri "$newAPIURL/api/status"

$newAPISetup = Invoke-RestMethod -Uri "$newAPIURL/api/setup" -TimeoutSec 10
if (-not $newAPISetup.data.status) {
    $setupBody = @{
        username = $environment["NEW_API_OWNER_USERNAME"]
        password = $environment["NEW_API_OWNER_PASSWORD"]
        confirmPassword = $environment["NEW_API_OWNER_PASSWORD"]
        SelfUseModeEnabled = $false
        DemoSiteEnabled = $false
    }
    $setupResult = Invoke-RestMethod -Method Post -Uri "$newAPIURL/api/setup" -ContentType "application/json" -Body (ConvertTo-CompactJson $setupBody) -TimeoutSec 30
    if (-not $setupResult.success) { throw "New API 所有者初始化失败" }
}

$authStatus = Invoke-RestMethod -Uri "$manyRouterURL/api/v1/auth/status" -TimeoutSec 10
if (-not $authStatus.initialized) {
    $ownerBody = @{
        username = $environment["MANYROUTER_OWNER_USERNAME"]
        password = $environment["MANYROUTER_OWNER_PASSWORD"]
        setup_token = $environment["MANYROUTER_OPERATOR_TOKEN"]
    }
    Invoke-RestMethod -Method Post -Uri "$manyRouterURL/api/v1/auth/setup" -ContentType "application/json" -Body (ConvertTo-CompactJson $ownerBody) -TimeoutSec 30 | Out-Null
}

$operatorHeaders = Get-OperatorHeaders -Token $environment["MANYROUTER_OPERATOR_TOKEN"]
$sites = Invoke-RestMethod -Uri "$manyRouterURL/api/v1/ops/sites?limit=100" -Headers $operatorHeaders -TimeoutSec 15
$site = @($sites.items | Where-Object { $_.code -eq $environment["MANYROUTER_SITE_CODE"] }) | Select-Object -First 1
if (-not $site) {
    $siteBody = @{
        code = $environment["MANYROUTER_SITE_CODE"]
        name = $environment["MANYROUTER_SITE_NAME"]
        new_api_base_url = "http://new-api:3000"
        new_api_access_token = $environment["MANYROUTER_SYNC_TOKEN"]
    }
    $headers = Get-OperatorHeaders -Token $environment["MANYROUTER_OPERATOR_TOKEN"] -IdempotencyKey "bootstrap-site-$($environment['MANYROUTER_SITE_CODE'])"
    $site = Invoke-RestMethod -Method Post -Uri "$manyRouterURL/api/v1/sites" -Headers $headers -ContentType "application/json" -Body (ConvertTo-CompactJson $siteBody) -TimeoutSec 30
}

$checkHeaders = Get-OperatorHeaders -Token $environment["MANYROUTER_OPERATOR_TOKEN"] -IdempotencyKey "bootstrap-check-$($site.id)"
$runtime = Invoke-RestMethod -Method Post -Uri "$manyRouterURL/api/v1/ops/runtime-health/$($site.id)/check" -Headers $checkHeaders -ContentType "application/json" -Body "{}" -TimeoutSec 60
if (-not $runtime.compatibility -or $runtime.compatibility.verdict -ne "compatible") {
    throw "站点兼容检查未通过：$(@($runtime.reasons.message) -join '；')"
}

if ([string]::IsNullOrWhiteSpace($environment["MANYROUTER_SITE_TOKEN"])) {
    $tokens = Invoke-RestMethod -Uri "$manyRouterURL/api/v1/ops/site-product-tokens?site_id=$($site.id)" -Headers $operatorHeaders -TimeoutSec 15
    if (@($tokens.items | Where-Object { $_.status -eq "active" }).Count -gt 0) {
        throw "站点已有产品令牌，但 deploy/.env 没有保存明文；请在控制台轮换后再继续。"
    }
    $tokenHeaders = Get-OperatorHeaders -Token $environment["MANYROUTER_OPERATOR_TOKEN"] -IdempotencyKey "bootstrap-product-$($site.id)"
    $issued = Invoke-RestMethod -Method Post -Uri "$manyRouterURL/api/v1/ops/sites/$($site.id)/product-tokens" -Headers $tokenHeaders -ContentType "application/json" -Body '{"reason":"new_site_bootstrap"}' -TimeoutSec 30
    Set-ReleaseEnvironmentValue -Key "MANYROUTER_SITE_TOKEN" -Value $issued.token
    $environment = Get-ReleaseEnvironment
    Invoke-ReleaseCompose up -d --force-recreate new-api
    Wait-ReleaseEndpoint -Uri "$newAPIURL/api/status"
}

if (-not $SkipSmoke) {
    & (Join-Path $PSScriptRoot "smoke.ps1")
}

[pscustomobject]@{
    Project = $environment["COMPOSE_PROJECT_NAME"]
    ManyRouter = $manyRouterURL
    NewAPI = $newAPIURL
    Site = $environment["MANYROUTER_SITE_CODE"]
    Configuration = $script:ReleaseEnvPath
    Result = "初始化完成"
}
