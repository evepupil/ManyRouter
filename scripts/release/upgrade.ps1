param(
    [Parameter(Mandatory)][string]$ManyRouterImage,
    [Parameter(Mandatory)][string]$NewAPIImage,
    [Parameter(Mandatory)][string]$ManyRouterVersion,
    [Parameter(Mandatory)][string]$ManyRouterCommit,
    [Parameter(Mandatory)][string]$NewAPIVersion,
    [string]$EnvironmentFile = ""
)

if ($EnvironmentFile) { $env:MANYROUTER_RELEASE_ENV_FILE = $EnvironmentFile }
. (Join-Path $PSScriptRoot "common.ps1")
& (Join-Path $PSScriptRoot "preflight.ps1") | Out-Null
$environment = Get-ReleaseEnvironment
$backupRoot = Join-Path $script:ReleaseDeployDir "backups"
[System.IO.Directory]::CreateDirectory($backupRoot) | Out-Null
$releaseDirectory = Join-Path $backupRoot ([DateTime]::UtcNow.ToString("yyyyMMdd-HHmmss"))
[System.IO.Directory]::CreateDirectory($releaseDirectory) | Out-Null
Copy-Item -LiteralPath $script:ReleaseEnvPath -Destination (Join-Path $releaseDirectory ".env.before")

function Assert-DatabaseIdentifier([string]$Value) {
    if ($Value -notmatch "^[A-Za-z_][A-Za-z0-9_]*$") { throw "数据库标识格式无效" }
}

function Backup-Database {
    param([string]$Service, [string]$User, [string]$Database, [string]$FileName)
    Assert-DatabaseIdentifier $User
    Assert-DatabaseIdentifier $Database
    $containerID = (Invoke-ReleaseCompose ps -q $Service | Select-Object -First 1).Trim()
    if (-not $containerID) { throw "$Service 未运行" }
    $temporary = "/tmp/$FileName"
    $destination = ConvertTo-ReleaseWSLPath (Join-Path $releaseDirectory $FileName)
    Invoke-ReleaseDocker exec $containerID pg_dump -U $User -d $Database -Fc -f $temporary | Out-Null
    Invoke-ReleaseDocker cp "${containerID}:$temporary" $destination | Out-Null
    Invoke-ReleaseDocker exec $containerID rm -f $temporary | Out-Null
}

Backup-Database -Service "manyrouter-db" -User $environment["MANYROUTER_DB_USER"] -Database $environment["MANYROUTER_DB_NAME"] -FileName "manyrouter.dump"
Backup-Database -Service "new-api-db" -User $environment["NEW_API_DB_ADMIN_USER"] -Database $environment["NEW_API_DB_NAME"] -FileName "new-api.dump"
Backup-Database -Service "new-api-db" -User $environment["NEW_API_DB_ADMIN_USER"] -Database $environment["NEW_API_LOG_DB_NAME"] -FileName "new-api-log.dump"

$release = [ordered]@{
    created_at = [DateTime]::UtcNow.ToString("o")
    project = $environment["COMPOSE_PROJECT_NAME"]
    previous_manyrouter_image = $environment["MANYROUTER_IMAGE"]
    previous_new_api_image = $environment["NEW_API_IMAGE"]
    target_manyrouter_image = $ManyRouterImage
    target_new_api_image = $NewAPIImage
    target_manyrouter_version = $ManyRouterVersion
    target_manyrouter_commit = $ManyRouterCommit
    target_new_api_version = $NewAPIVersion
    database_backups = @("manyrouter.dump", "new-api.dump", "new-api-log.dump")
}
[System.IO.File]::WriteAllText((Join-Path $releaseDirectory "release.json"), ($release | ConvertTo-Json -Depth 5), [System.Text.UTF8Encoding]::new($false))

try {
    foreach ($image in @($ManyRouterImage, $NewAPIImage)) {
        try { Invoke-ReleaseDocker image inspect $image | Out-Null } catch { Invoke-ReleaseDocker pull $image | Out-Null }
    }
    Set-ReleaseEnvironmentValue -Key "MANYROUTER_IMAGE" -Value $ManyRouterImage
    Set-ReleaseEnvironmentValue -Key "NEW_API_IMAGE" -Value $NewAPIImage
    Set-ReleaseEnvironmentValue -Key "MANYROUTER_BUILD_VERSION" -Value $ManyRouterVersion
    Set-ReleaseEnvironmentValue -Key "MANYROUTER_BUILD_COMMIT" -Value $ManyRouterCommit
    Set-ReleaseEnvironmentValue -Key "NEW_API_BUILD_VERSION" -Value $NewAPIVersion
    Invoke-ReleaseCompose up -d
    & (Join-Path $PSScriptRoot "smoke.ps1") | Out-Null
    [System.IO.File]::WriteAllText((Join-Path $releaseDirectory "result.txt"), "upgrade_succeeded`n", [System.Text.UTF8Encoding]::new($false))
} catch {
    [System.IO.File]::WriteAllText((Join-Path $releaseDirectory "result.txt"), "upgrade_failed_rollback_started`n", [System.Text.UTF8Encoding]::new($false))
    & (Join-Path $PSScriptRoot "rollback.ps1") -ReleaseDirectory $releaseDirectory | Out-Null
    throw
}

[pscustomobject]@{ ReleaseDirectory = $releaseDirectory; Result = "升级完成" }
