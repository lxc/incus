# Installation folder in ProgramData
$destFolder = "C:\ProgramData\Incus-Agent"
$agentExecutable = "incus-agent.exe"

$targetDrive = Get-WmiObject -Class Win32_Volume | Where-Object { $_.Label -eq "incus-agent" }

if (!$targetDrive) {
    Write-Host "Drive containing the agent was not found."
    Write-Host "Searching if the agent is already installed..."

    if (!(Test-Path $destFolder)) {
        Write-Host "$destFolder was not found."
        exit 1
    }
    elseif (!(Test-Path "$destFolder\$agentExecutable")) {
        Write-Host "$destFolder\$agentExecutable was not found."
        exit 1
    }
}
else {
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
        -Recurse `
        -Path "$($targetDrive.Name)*" `
        -Destination $destFolder `
        -Exclude '*.ps1', '*.bat' `
        -Force

    if (!$?) {
        Write-Host "Failed to copy the agent files."
        exit 1
    }
    
    Write-Host "Ejecting CD-ROM..."
    (New-Object -ComObject Shell.Application).Namespace(17).ParseName($targetDrive.DriveLetter).InvokeVerb("Eject")   
}

# By default, services are ran in C:\Windows\System32. Changing the location so the agent finds the certificates and keys.
Set-Location -Path $destFolder

# Dumb search for firewall rule assuming the name of the rule is "incus-agent-service".
if (!(Get-NetFirewallRule -Name "incus-agent-service" -ErrorAction SilentlyContinue)) {
    New-NetFirewallRule -Name "incus-agent-service" -DisplayName "Allow Port 8443 for Incus-Agent" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 8443
}

Write-Host "Running the agent..."

& .\incus-agent.exe --service