# WSL distro を Windows 起動時に起こし、systemd user サービスが動ける状態を維持する。
# 既定はログイン前起動 (AtStartup)。動かない場合は -AtLogOn でフォールバックする。
[CmdletBinding()]
param(
    [string]$Distro = "Debian",
    [string]$TaskName = "web-cursor-agent-wsl",
    [int]$DelaySeconds = 45,
    [switch]$AtLogOn
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Resolve-WslExecutable {
    $candidates = @(
        "${env:ProgramFiles}\WSL\wsl.exe",
        "${env:SystemRoot}\System32\wsl.exe"
    )
    foreach ($path in $candidates) {
        if (Test-Path -LiteralPath $path) {
            return $path
        }
    }
    throw "wsl.exe was not found. Install or update WSL first."
}

$wsl = Resolve-WslExecutable
$argument = "-d $Distro -u root -- sleep infinity"

if ($AtLogOn) {
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
    $modeLabel = "AtLogOn"
} else {
    $trigger = New-ScheduledTaskTrigger -AtStartup
    $modeLabel = "AtStartup"
}

if ($DelaySeconds -gt 0) {
    $trigger.Delay = "PT${DelaySeconds}S"
}

$action = New-ScheduledTaskAction -Execute $wsl -Argument $argument
$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -StartWhenAvailable `
    -ExecutionTimeLimit ([TimeSpan]::Zero) `
    -RestartCount 3 `
    -RestartInterval (New-TimeSpan -Minutes 1)

$existing = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($null -ne $existing) {
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
}

# パスワードを保存せずログイン無し実行を試す (S4U)。失敗したらパスワード入力へフォールバックする。
$registered = $false
try {
    $principal = New-ScheduledTaskPrincipal `
        -UserId "$env:USERDOMAIN\$env:USERNAME" `
        -LogonType S4U `
        -RunLevel Limited
    Register-ScheduledTask `
        -TaskName $TaskName `
        -Action $action `
        -Trigger $trigger `
        -Principal $principal `
        -Settings $settings `
        -Description "Keep WSL distro '$Distro' running so web-cursor-agent can autostart." `
        | Out-Null
    $registered = $true
    Write-Host "Registered task '$TaskName' with LogonType=S4U ($modeLabel)."
} catch {
    Write-Warning "S4U registration failed: $($_.Exception.Message)"
    Write-Warning "Retrying with a stored password (Run whether user is logged on or not)."
}

if (-not $registered) {
    $secure = Read-Host -AsSecureString -Prompt "Password for $env:USERDOMAIN\$env:USERNAME"
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
    try {
        $plain = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }

    Register-ScheduledTask `
        -TaskName $TaskName `
        -Action $action `
        -Trigger $trigger `
        -User "$env:USERDOMAIN\$env:USERNAME" `
        -Password $plain `
        -Settings $settings `
        -Description "Keep WSL distro '$Distro' running so web-cursor-agent can autostart." `
        | Out-Null
    Write-Host "Registered task '$TaskName' with stored password ($modeLabel)."
}

Write-Host "Executable: $wsl"
Write-Host "Arguments : $argument"
Write-Host "Delay     : ${DelaySeconds}s"
Write-Host "Run now with: Start-ScheduledTask -TaskName '$TaskName'"
Write-Host "Check WSL with: & '$wsl' -l -v"
