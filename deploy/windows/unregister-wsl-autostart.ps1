# register-wsl-autostart.ps1 が作った WSL keep-alive タスクを削除する。
[CmdletBinding()]
param(
    [string]$TaskName = "web-cursor-agent-wsl"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$existing = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($null -eq $existing) {
    Write-Host "Task '$TaskName' was not found."
    return
}

Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
Write-Host "Removed task '$TaskName'."
