param([Parameter(Mandatory)][string]$ReleaseDirectory)

. (Join-Path $PSScriptRoot "common.ps1")

$backupRoot = [System.IO.Path]::GetFullPath((Join-Path $script:ReleaseDeployDir "backups"))
$resolved = [System.IO.Path]::GetFullPath($ReleaseDirectory)
$prefix = $backupRoot.TrimEnd([System.IO.Path]::DirectorySeparatorChar) + [System.IO.Path]::DirectorySeparatorChar
if (-not $resolved.StartsWith($prefix, [System.StringComparison]::OrdinalIgnoreCase)) {
    throw "回退目录必须位于 deploy/backups"
}
$previousEnvironment = Join-Path $resolved ".env.before"
if (-not (Test-Path -LiteralPath $previousEnvironment -PathType Leaf)) { throw "回退目录缺少 .env.before" }
Copy-Item -LiteralPath $previousEnvironment -Destination $script:ReleaseEnvPath -Force
& (Join-Path $PSScriptRoot "preflight.ps1") | Out-Null
Invoke-ReleaseCompose up -d
& (Join-Path $PSScriptRoot "smoke.ps1") | Out-Null
[System.IO.File]::WriteAllText((Join-Path $resolved "rollback-result.txt"), "application_rollback_succeeded`n", [System.Text.UTF8Encoding]::new($false))

[pscustomobject]@{
    ReleaseDirectory = $resolved
    DatabaseRestore = "未执行"
    Result = "应用回退完成"
}
