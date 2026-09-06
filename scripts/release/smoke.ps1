param(
    [string]$Model = "",
    [string]$NewAPIKey = "",
    [string]$EnvironmentFile = ""
)

if ($EnvironmentFile) { $env:MANYROUTER_RELEASE_ENV_FILE = $EnvironmentFile }
. (Join-Path $PSScriptRoot "common.ps1")

$environment = Get-ReleaseEnvironment
$manyRouterURL = "http://127.0.0.1:$($environment['MANYROUTER_PORT'])"
$newAPIURL = "http://127.0.0.1:$($environment['NEW_API_PORT'])"
$null = Wait-ReleaseEndpoint -Uri "$manyRouterURL/api/v1/healthz"
$null = Wait-ReleaseEndpoint -Uri "$newAPIURL/api/status"
$operatorHeaders = Get-OperatorHeaders -Token (Get-RequiredReleaseValue -Environment $environment -Key "MANYROUTER_OPERATOR_TOKEN")
$syncHeaders = Get-OperatorHeaders -Token (Get-RequiredReleaseValue -Environment $environment -Key "MANYROUTER_SYNC_TOKEN")
$productHeaders = Get-OperatorHeaders -Token (Get-RequiredReleaseValue -Environment $environment -Key "MANYROUTER_SITE_TOKEN")

$health = Invoke-RestMethod -Uri "$manyRouterURL/api/v1/healthz" -TimeoutSec 10
$status = Invoke-RestMethod -Uri "$newAPIURL/api/status" -TimeoutSec 10
$capabilities = Invoke-RestMethod -Uri "$newAPIURL/api/manyrouter/sync/capabilities" -Headers $syncHeaders -TimeoutSec 15
if (-not $capabilities.success -or -not $capabilities.data.features.atomic_apply -or -not $capabilities.data.features.log_read) {
    throw "New API 窄权限能力检查未通过"
}
$stateBefore = Invoke-RestMethod -Uri "$newAPIURL/api/manyrouter/sync/state" -Headers $syncHeaders -TimeoutSec 15
$stateAfter = Invoke-RestMethod -Uri "$newAPIURL/api/manyrouter/sync/state" -Headers $syncHeaders -TimeoutSec 15
if ($stateBefore.data.state_hash -ne $stateAfter.data.state_hash) { throw "只读检查期间受管状态发生变化" }

$end = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
$start = $end - 3600
$logs = Invoke-RestMethod -Uri "$newAPIURL/api/manyrouter/sync/logs?type=2&start_timestamp=$start&end_timestamp=$end&p=1&page_size=1" -Headers $syncHeaders -TimeoutSec 15
if (-not $logs.success) { throw "窄权限日志读取失败" }

$sites = Invoke-RestMethod -Uri "$manyRouterURL/api/v1/ops/sites?limit=100" -Headers $operatorHeaders -TimeoutSec 15
$site = @($sites.items | Where-Object { $_.code -eq $environment["MANYROUTER_SITE_CODE"] }) | Select-Object -First 1
if (-not $site) { throw "组合中的站点记录不存在" }
$checkHeaders = Get-OperatorHeaders -Token $environment["MANYROUTER_OPERATOR_TOKEN"] -IdempotencyKey "smoke-$([guid]::NewGuid())"
$runtime = Invoke-RestMethod -Method Post -Uri "$manyRouterURL/api/v1/ops/runtime-health/$($site.id)/check" -Headers $checkHeaders -ContentType "application/json" -Body "{}" -TimeoutSec 60
if (-not $runtime.compatibility -or $runtime.compatibility.verdict -ne "compatible") { throw "运行状态兼容检查未通过" }
$product = Invoke-RestMethod -Uri "$manyRouterURL/api/v1/site/capabilities" -Headers $productHeaders -TimeoutSec 15
$metrics = Invoke-WebRequest -UseBasicParsing -Uri "$manyRouterURL/metrics" -Headers $operatorHeaders -TimeoutSec 15
if (-not $metrics.Content.Contains("manyrouter_database_up 1")) { throw "运行指标没有确认数据库可用" }

$modelCall = "未执行"
if ($Model -and $NewAPIKey) {
    $callHeaders = @{ Authorization = "Bearer $NewAPIKey" }
    $callBody = @{ model = $Model; messages = @(@{ role = "user"; content = "ping" }); max_tokens = 1 }
    Invoke-RestMethod -Method Post -Uri "$newAPIURL/v1/chat/completions" -Headers $callHeaders -ContentType "application/json" -Body (ConvertTo-CompactJson $callBody) -TimeoutSec 90 | Out-Null
    $modelCall = "通过"
}

$evidenceDirectory = Join-Path $script:ReleaseDeployDir "evidence"
[System.IO.Directory]::CreateDirectory($evidenceDirectory) | Out-Null
$evidence = [ordered]@{
    checked_at = [DateTime]::UtcNow.ToString("o")
    project = $environment["COMPOSE_PROJECT_NAME"]
    manyrouter_health = $health.status
    manyrouter_build = $runtime.compatibility.catalog_version
    new_api_version = $status.data.version
    contract_version = $capabilities.data.contract_version
    database_type = $capabilities.data.database_type
    managed_state_hash = $stateAfter.data.state_hash
    managed_channels = @($stateAfter.data.channels).Count
    scoped_log_records = @($logs.data.items).Count
    site_id = $site.id
    compatibility = $runtime.compatibility.verdict
    product_contract = $product.contract_version
    model_call = $modelCall
}
$evidencePath = Join-Path $evidenceDirectory ("smoke-" + [DateTime]::UtcNow.ToString("yyyyMMdd-HHmmss") + ".json")
[System.IO.File]::WriteAllText($evidencePath, ($evidence | ConvertTo-Json -Depth 10), [System.Text.UTF8Encoding]::new($false))

[pscustomobject]@{
    Project = $environment["COMPOSE_PROJECT_NAME"]
    NewAPI = $status.data.version
    Contract = $capabilities.data.contract_version
    Compatibility = $runtime.compatibility.verdict
    ModelCall = $modelCall
    Evidence = $evidencePath
    Result = "通过"
}
