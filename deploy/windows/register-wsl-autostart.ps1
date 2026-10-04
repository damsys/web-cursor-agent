# Keep the WSL distro alive at Windows startup so systemd user services can run.
# Default trigger is AtStartup (before interactive logon). Use -AtLogOn as fallback.
# If registration returns Access Denied, run this script from an elevated PowerShell.
[CmdletBinding()]
param(
    [string]$Distro = "Debian",
    [string]$TaskName = "web-cursor-agent-wsl",
    [int]$DelaySeconds = 45,
    [switch]$AtLogOn
)

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

function ConvertFrom-SecureStringPlain {
    param(
        [Parameter(Mandatory = $true)]
        [SecureString]$Secure
    )
    $bstr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($Secure)
    try {
        return [Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
    } finally {
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }
}

$wsl = Resolve-WslExecutable
$argument = "-d $Distro -u root -- sleep infinity"
$description = "Keep WSL distro '$Distro' running so web-cursor-agent can autostart."

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

# Try password-less S4U first; on failure, register with a stored password.
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
        -Description $description `
        | Out-Null
    Write-Host "Registered task '$TaskName' with LogonType=S4U ($modeLabel)."
} catch {
    Write-Warning "S4U registration failed: $($_.Exception.Message)"
    Write-Warning "Retrying with a stored password (Run whether user is logged on or not)."
    Write-Warning "If this also fails with Access Denied, re-run from an elevated PowerShell."

    $secure = Read-Host -AsSecureString -Prompt "Password for $env:USERDOMAIN\$env:USERNAME"
    $plain = ConvertFrom-SecureStringPlain -Secure $secure

    Register-ScheduledTask `
        -TaskName $TaskName `
        -Action $action `
        -Trigger $trigger `
        -User "$env:USERDOMAIN\$env:USERNAME" `
        -Password $plain `
        -Settings $settings `
        -Description $description `
        | Out-Null
    Write-Host "Registered task '$TaskName' with stored password ($modeLabel)."
}

Write-Host "Executable: $wsl"
Write-Host "Arguments : $argument"
Write-Host "Delay     : ${DelaySeconds}s"
Write-Host "Run now with: Start-ScheduledTask -TaskName '$TaskName'"
Write-Host "Check WSL with: & '$wsl' -l -v"
