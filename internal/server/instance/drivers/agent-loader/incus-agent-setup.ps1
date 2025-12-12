# Variables setup
# Installation folder in ProgramData
$destFolder = "C:\ProgramData\Incus-Agent"
$agentExecutable = "incus-agent.exe"
$serviceName = "Incus-Agent"
$serviceDisplayName = "Incus Agent Service"
$serviceDescription = "Incus Agent Service"

function ExitSetup {
    # Recursively delete the old agent as we only want the agent to run if the CDROM is present.
    # Failsafe in case it was not deleted on shutdown.
    # Close the firewall.
    Remove-NetFirewallRule -Name $serviceName -ErrorAction SilentlyContinue
    # Stop the service in case it was running.
    Stop-Service $serviceName -Force
    # Delete the service.
    sc.exe delete $serviceName
    # Delete all files
    Remove-Item -Path "$destFolder" -Recurse -Force
}

$targetDrive = Get-WmiObject -Class Win32_Volume | Where-Object { $_.Label -eq "incus-agent" }

if (!$targetDrive) {
    Write-Host "Drive containing the agent was not found."
    ExitSetup
}

Write-Host "Drive containing the agent was found: $($targetDrive.DriveLetter)"

if (!(Test-Path $destFolder)) {
    Write-Host "Creating $destFolder..."
    New-Item -ItemType Directory -Path $destFolder -Force | Out-Null
    
    if (!$?) {
        Write-Host "Could not create $destFolder..."
        ExitSetup
    }
}

Write-Host "Copying the content of the CD-ROM to $destFolder..."
Copy-Item `
    -Recurse `
    -Path "$($targetDrive.Name)*" `
    -Destination $destFolder `
    -Exclude '*.ps1', '*.bat' `
    -Force

if (!$?) {
    Write-Host "Failed to copy the agent files."
    ExitSetup
}

Write-Host "Ejecting CD-ROM..."
(New-Object -ComObject Shell.Application).Namespace(17).ParseName($targetDrive.DriveLetter).InvokeVerb("Eject")   

# Dumb search for firewall rule assuming the name of the rule is "$serviceName".
if (!(Get-NetFirewallRule -Name "$serviceName" -ErrorAction SilentlyContinue)) {
    New-NetFirewallRule -Name "$serviceName" -DisplayName "Allow Port 8443 for Incus-Agent" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 8443
}

$serviceCommand = "`"$destFolder\$agentExecutable`" --service --secrets-location $destFolder"

if (Get-Service -Name $serviceName -ErrorAction SilentlyContinue) {
    Write-Host "Service exists. Updating configuration..."

    # Do not Out-Null to see if there is any error doing the following command as it is important.
    sc.exe config $serviceName binPath= "$serviceCommand" DisplayName= "$serviceDisplayName"
    sc.exe description $serviceName "$serviceDescription" | Out-Null

    Set-Service -Name $serviceName -StartupType Manual

    Write-Host "Service '$serviceName' updated successfully."
}
else {
    Write-Host "Service does not exist. Creating new service..."

    New-Service -Name $serviceName -BinaryPathName $serviceCommand -DisplayName $serviceDisplayName -Description $serviceDescription -StartupType Manual

    Write-Host "Service '$serviceName' created successfully."
}

Restart-Service $serviceName -Force