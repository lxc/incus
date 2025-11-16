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

$serviceName = "Incus-Agent"
$serviceDisplayName = "Incus Agent Service"
$serviceDescription = "Incus Agent Service"

# Hack to run a PowerShell script as a service without a third party tool.
# The bat file only contains a line to run the actual PowerShell script.
$serviceCommand = "`"$destFolder\incus-agent-setup.bat`""

if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
    Write-Host "Service exists. Updating configuration..."

    # Do not Out-Null to see if there is any error doing the following command as it is important.
    sc.exe config $serviceName binPath= "$serviceCommand" DisplayName= "$serviceDisplayName"
    sc.exe description $serviceName "$serviceDescription" | Out-Null

    Set-Service -Name $serviceName -StartupType Automatic

    Write-Host "Service '$serviceName' updated successfully."
}
else {
    
    Write-Host "Service does not exist. Creating new service..."

    New-Service -Name $serviceName -BinaryPathName $serviceCommand -DisplayName $serviceDisplayName -Description $serviceDescription -StartupType Automatic

    Write-Host "Service '$serviceName' created successfully."
}

Restart-Service $serviceName -Force