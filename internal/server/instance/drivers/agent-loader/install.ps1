$IsAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

if (!$IsAdmin) {
    Write-Host "This script requires local administrator privilege. Rerun the script in an administrator PowerShell."
    exit 1
}

$destFolder = "C:\Program Files\Incus-Agent"

$targetDrive = Get-WmiObject -Class Win32_Volume | Where-Object { $_.Label -eq "incus-agent" }

if (!$targetDrive) {
    Write-Host "Drive containing the agent was not found."
    exit 1
}

Write-Host "Drive containing the agent was found: $($targetDrive.DriveLetter)"

if (!(Test-Path $destFolder)) {
    Write-Host "Creating $destFolder..."
    New-Item -ItemType Directory -Path $destFolder -Force | Out-Null
    
    if (!$?) {
        Write-Host "Could not create $destFolder..."
        exit 1
    }
}

Write-Host "Copying the content of the CD-ROM to $destFolder..."
Copy-Item `
    -Path "$($targetDrive.DriveLetter)\incus-agent-setup.*" `
    -Destination "$destFolder\" `
    -Force

if (!$?) {
    Write-Host "Failed to copy the agent files."
    exit 1
}

# Override the scheduled task even if it exists
$destFolder = "C:\Program Files\Incus-Agent"
$taskFile = "$destFolder\incus-agent-setup.ps1"
$taskAction = New-ScheduledTaskAction -Execute "C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe" -Argument "-ExecutionPolicy Bypass -File `"$taskFile`""
$taskTrigger = New-ScheduledTaskTrigger -AtStartup
$taskPrincipal = New-ScheduledTaskPrincipal -UserID "NT AUTHORITY\SYSTEM" -LogonType ServiceAccount -RunLevel Highest
Register-ScheduledTask -Action $taskAction -Trigger $taskTrigger -Principal $taskPrincipal -TaskName "Incus Agent Setup" -Description "Every setup required for the Incus agent including copying the files, opening the firewall, etc." -Force

# Start the PowerShell script to simulate start up
& "$taskFile"