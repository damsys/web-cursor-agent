# Remove the WSL keep-alive task created by register-wsl-autostart.ps1.
[CmdletBinding()]
param(
    [string]$TaskName = "web-cursor-agent-wsl"
)

$ErrorActionPreference = "Stop"

$existing = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($null -eq $existing) {
    Write-Host "Task '$TaskName' was not found."
    return
}

Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
Write-Host "Removed task '$TaskName'."
