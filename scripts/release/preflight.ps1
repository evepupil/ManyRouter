param(
    [switch]$AllowMissingLocalImages,
    [string]$EnvironmentFile = ""
)

if ($EnvironmentFile) { $env:MANYROUTER_RELEASE_ENV_FILE = $EnvironmentFile }

. (Join-Path $PSScriptRoot "common.ps1")

$environment = Get-ReleaseEnvironment
$required = @(
    "COMPOSE_PROJECT_NAME", "MANYROUTER_RELEASE_CHANNEL", "POSTGRES_IMAGE", "MANYROUTER_IMAGE",
    "NEW_API_IMAGE", "MANYROUTER_PORT", "NEW_API_PORT", "MANYROUTER_DB_USER",
    "MANYROUTER_DB_NAME", "MANYROUTER_DB_PASSWORD", "MANYROUTER_MASTER_KEY",
    "MANYROUTER_OPERATOR_TOKEN", "MANYROUTER_OWNER_USERNAME", "MANYROUTER_OWNER_PASSWORD",
    "NEW_API_DB_ADMIN_USER", "NEW_API_DB_ADMIN_PASSWORD", "NEW_API_DB_NAME", "NEW_API_DB_USER",
    "NEW_API_DB_PASSWORD", "NEW_API_LOG_DB_NAME", "NEW_API_LOG_DB_USER", "NEW_API_LOG_DB_PASSWORD",
    "NEW_API_SESSION_SECRET", "NEW_API_OWNER_USERNAME", "NEW_API_OWNER_PASSWORD",
    "MANYROUTER_SYNC_TOKEN", "MANYROUTER_SITE_CODE", "MANYROUTER_SITE_NAME"
)
foreach ($key in $required) {
    $value = Get-RequiredReleaseValue -Environment $environment -Key $key
    if ($value -like "replace_with_*") { throw "$key 仍是模板值" }
}

$project = $environment["COMPOSE_PROJECT_NAME"]
if ($project -notmatch "^[a-z0-9][a-z0-9_-]{2,62}$") { throw "COMPOSE_PROJECT_NAME 格式无效" }
foreach ($portKey in @("MANYROUTER_PORT", "NEW_API_PORT")) {
    $port = 0
    if (-not [int]::TryParse($environment[$portKey], [ref]$port) -or $port -lt 1024 -or $port -gt 65535) {
        throw "$portKey 必须是 1024 到 65535 的端口"
    }
}
if ($environment["MANYROUTER_PORT"] -eq $environment["NEW_API_PORT"]) { throw "两个服务不能使用同一主机端口" }
if ($environment["MANYROUTER_MASTER_KEY"].Length -lt 44) { throw "ManyRouter 主密钥长度不足" }
foreach ($key in @("MANYROUTER_OPERATOR_TOKEN", "NEW_API_SESSION_SECRET", "MANYROUTER_SYNC_TOKEN")) {
    if ($environment[$key].Length -lt 32) { throw "$key 长度不足" }
}
if ($environment["MANYROUTER_RELEASE_CHANNEL"] -eq "stable") {
    foreach ($key in @("POSTGRES_IMAGE", "MANYROUTER_IMAGE", "NEW_API_IMAGE")) {
        if ($environment[$key] -notmatch "@sha256:[0-9a-f]{64}$") { throw "稳定发布要求 $key 使用镜像摘要" }
    }
}

Invoke-ReleaseWSL -Arguments @("docker", "version", "--format", "{{.Server.Version}}") | Out-Null
Invoke-ReleaseWSL -Arguments @("docker", "compose", "version", "--short") | Out-Null
Invoke-ReleaseCompose config --quiet | Out-Null

$ownedPorts = [System.Collections.Generic.HashSet[int]]::new()
try {
    foreach ($line in @(Invoke-ReleaseCompose ps --format json)) {
        if ([string]::IsNullOrWhiteSpace($line)) { continue }
        $container = $line | ConvertFrom-Json
        foreach ($publisher in @($container.Publishers)) {
            if ($publisher.PublishedPort) { [void]$ownedPorts.Add([int]$publisher.PublishedPort) }
        }
    }
} catch {
    throw "无法读取当前发布组合的端口状态"
}
foreach ($portKey in @("MANYROUTER_PORT", "NEW_API_PORT")) {
    $port = [int]$environment[$portKey]
    if ($ownedPorts.Contains($port)) { continue }
    $windowsListener = Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue
    $linuxListener = @(Invoke-ReleaseWSL -Arguments @("sh", "-lc", "ss -H -ltn 'sport = :$port'"))
    if ($windowsListener -or $linuxListener.Count -gt 0) { throw "$portKey=$port 已被其他进程占用" }
}

if (-not $AllowMissingLocalImages) {
    foreach ($key in @("POSTGRES_IMAGE", "MANYROUTER_IMAGE", "NEW_API_IMAGE")) {
        Invoke-ReleaseDocker image inspect $environment[$key] | Out-Null
    }
}

$existingVolumes = @(Invoke-ReleaseDocker volume ls --filter "name=$project" --format "{{.Name}}")
$backupDirectory = Join-Path $script:ReleaseDeployDir "backups"
$latestBackup = "无"
if (Test-Path -LiteralPath $backupDirectory) {
    $latest = Get-ChildItem -LiteralPath $backupDirectory -Directory | Sort-Object LastWriteTime -Descending | Select-Object -First 1
    if ($latest) { $latestBackup = $latest.Name }
}

[pscustomobject]@{
    Project = $project
    ReleaseChannel = $environment["MANYROUTER_RELEASE_CHANNEL"]
    ManyRouterPort = [int]$environment["MANYROUTER_PORT"]
    NewAPIPort = [int]$environment["NEW_API_PORT"]
    ExistingVolumes = $existingVolumes.Count
    LatestBackup = $latestBackup
    Result = "通过"
}
