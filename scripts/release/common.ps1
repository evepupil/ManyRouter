Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$script:ReleaseRepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$script:ReleaseDeployDir = Join-Path $script:ReleaseRepoRoot "deploy"
$configuredEnvironment = [Environment]::GetEnvironmentVariable("MANYROUTER_RELEASE_ENV_FILE")
$script:ReleaseEnvPath = if ([string]::IsNullOrWhiteSpace($configuredEnvironment)) {
    Join-Path $script:ReleaseDeployDir ".env"
} else {
    [System.IO.Path]::GetFullPath($configuredEnvironment, $script:ReleaseRepoRoot)
}
$script:ReleaseWSLDistro = "Ubuntu-24.04"
$script:ReleaseWSLRoot = "/mnt/c/code/ManyRouter"

function Get-ReleaseEnvironment {
    param([string]$Path = $script:ReleaseEnvPath)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "部署配置不存在：$Path"
    }
    $values = [System.Collections.Generic.Dictionary[string, string]]::new([System.StringComparer]::Ordinal)
    foreach ($line in [System.IO.File]::ReadAllLines($Path)) {
        $trimmed = $line.Trim()
        if ($trimmed.Length -eq 0 -or $trimmed.StartsWith("#")) { continue }
        $separator = $line.IndexOf("=")
        if ($separator -lt 1) { throw "部署配置包含无效行：$line" }
        $key = $line.Substring(0, $separator).Trim()
        if ($key -notmatch "^[A-Z][A-Z0-9_]*$") { throw "部署配置键无效：$key" }
        $values[$key] = $line.Substring($separator + 1).Trim()
    }
    return $values
}

function Set-ReleaseEnvironmentValue {
    param(
        [Parameter(Mandatory)][string]$Key,
        [Parameter(Mandatory)][AllowEmptyString()][string]$Value,
        [string]$Path = $script:ReleaseEnvPath
    )
    if ($Key -notmatch "^[A-Z][A-Z0-9_]*$") { throw "部署配置键无效：$Key" }
    $lines = [System.Collections.Generic.List[string]]::new()
    $found = $false
    foreach ($line in [System.IO.File]::ReadAllLines($Path)) {
        if ($line -match "^$([regex]::Escape($Key))=") {
            $lines.Add("$Key=$Value")
            $found = $true
        } else {
            $lines.Add($line)
        }
    }
    if (-not $found) { $lines.Add("$Key=$Value") }
    [System.IO.File]::WriteAllLines($Path, $lines, [System.Text.UTF8Encoding]::new($false))
}

function Get-RequiredReleaseValue {
    param(
        [Parameter(Mandatory)][System.Collections.Generic.Dictionary[string, string]]$Environment,
        [Parameter(Mandatory)][string]$Key
    )
    if (-not $Environment.ContainsKey($Key) -or [string]::IsNullOrWhiteSpace($Environment[$Key])) {
        throw "$Key 未配置"
    }
    return $Environment[$Key]
}

function Invoke-ReleaseWSL {
    param([Parameter(Mandatory)][string[]]$Arguments)
    & wsl.exe -d $script:ReleaseWSLDistro --cd $script:ReleaseWSLRoot @Arguments
    if ($LASTEXITCODE -ne 0) { throw "WSL 命令执行失败：$($Arguments -join ' ')" }
}

function Invoke-ReleaseDocker {
    Invoke-ReleaseWSL -Arguments (@("docker") + $args)
}

function Invoke-ReleaseCompose {
    $environmentPath = ConvertTo-ReleaseWSLPath $script:ReleaseEnvPath
    Invoke-ReleaseWSL -Arguments (@("docker", "compose", "--env-file", $environmentPath, "-f", "deploy/compose.yaml") + $args)
}

function ConvertTo-ReleaseWSLPath {
    param([Parameter(Mandatory)][string]$Path)
    $full = [System.IO.Path]::GetFullPath($Path)
    if ($full -notmatch "^([A-Za-z]):\\(.*)$") { throw "无法转换为 WSL 路径：$Path" }
    $drive = $Matches[1].ToLowerInvariant()
    return "/mnt/$drive/" + $Matches[2].Replace("\", "/")
}

function New-HexSecret {
    param([int]$Bytes = 32)
    $buffer = [byte[]]::new($Bytes)
    [System.Security.Cryptography.RandomNumberGenerator]::Fill($buffer)
    return [Convert]::ToHexString($buffer).ToLowerInvariant()
}

function New-Base64Secret {
    param([int]$Bytes = 32)
    $buffer = [byte[]]::new($Bytes)
    [System.Security.Cryptography.RandomNumberGenerator]::Fill($buffer)
    return [Convert]::ToBase64String($buffer)
}

function New-URLSecret {
    param([int]$Bytes = 32)
    $buffer = [byte[]]::new($Bytes)
    [System.Security.Cryptography.RandomNumberGenerator]::Fill($buffer)
    return [Convert]::ToBase64String($buffer).TrimEnd("=").Replace("+", "-").Replace("/", "_")
}

function Wait-ReleaseEndpoint {
    param(
        [Parameter(Mandatory)][string]$Uri,
        [int]$Attempts = 60,
        [int]$DelaySeconds = 2
    )
    for ($attempt = 1; $attempt -le $Attempts; $attempt++) {
        try {
            $response = Invoke-WebRequest -UseBasicParsing -Uri $Uri -TimeoutSec 5
            if ($response.StatusCode -ge 200 -and $response.StatusCode -lt 300) { return }
        } catch {
            if ($attempt -eq $Attempts) { throw "服务未在限定时间内就绪：$Uri" }
        }
        Start-Sleep -Seconds $DelaySeconds
    }
}

function Get-OperatorHeaders {
    param([Parameter(Mandatory)][string]$Token, [string]$IdempotencyKey = "")
    $headers = @{ Authorization = "Bearer $Token" }
    if ($IdempotencyKey) { $headers["Idempotency-Key"] = $IdempotencyKey }
    return $headers
}

function ConvertTo-CompactJson {
    param([Parameter(Mandatory)]$Value)
    return $Value | ConvertTo-Json -Depth 20 -Compress
}
