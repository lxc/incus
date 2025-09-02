# Incus Agent Certificate Time Validation

## Overview
This version of the Incus agent includes certificate time validation checks to prevent authentication failures caused by system clock synchronization issues.

## Problem Solved
Previously, when a Windows VM's system clock was out of sync (particularly when behind), the agent would fail with cryptic "not authorized" errors during TLS authentication. This was because the certificate validity check would fail silently within the TLS stack.

## Implementation

### Changes Made

1. **api_1.0.go** (lines 222-245)
   - Added validation of system time against client certificate validity window
   - Checks both NotBefore and NotAfter times
   - Provides clear error messages with time differences in minutes

2. **network.go** (lines 76-94)
   - Added validation for the agent's own certificate
   - Ensures agent certificate is valid before starting TLS server

3. **server.go** (lines 108-129)
   - Enhanced authentication logging with certificate fingerprints
   - Added trust store debugging information
   - Improved certificate comparison logging

## Error Messages

### When System Clock is Behind
```
System time is 166 minutes before certificate validity period
CRITICAL: System clock appears to be behind. Certificate is not yet valid.
Please sync system time or wait for certificate to become valid.
Error: System time (2025-09-02T05:00:00Z) is before certificate validity (2025-09-02T07:46:02Z). Time sync required
```

### When Certificate Has Expired
```
System time is X minutes after certificate expiry
CRITICAL: Certificate has expired.
Error: Certificate expired on [date] (current time: [date])
```

### When Validation Passes
```
Certificate time validation passed
Agent certificate time validation passed
```

## Benefits

1. **Early Detection**: Issues are caught at startup rather than during connection attempts
2. **Clear Diagnostics**: Administrators immediately know the problem is time-related
3. **Actionable Errors**: Messages indicate exactly what needs to be fixed
4. **Prevents Silent Failures**: No more mysterious "not authorized" errors from time issues

## Testing

To test the time validation:

1. **Set incorrect time** (Windows PowerShell as Administrator):
   ```powershell
   Set-Date -Date "2025-09-02 05:00:00"
   ```

2. **Run the agent**:
   ```powershell
   C:\Incus\incus-agent.exe --debug
   ```

3. **Observe the error messages** indicating time sync is required

4. **Fix the time**:
   ```powershell
   w32tm /resync /force
   # Or manually set to current UTC:
   Set-Date -Date (Get-Date).ToUniversalTime()
   ```

5. **Run agent again** - should start successfully

## Windows Time Sync Issues

Common causes of time sync problems in Windows VMs:
- Windows Time service (w32time) not running
- No NTP server configured
- VM restored from snapshot with old time
- Hypervisor clock drift
- Time zone configuration issues

Quick fix:
```powershell
Start-Service w32time
w32tm /config /manualpeerlist:"time.windows.com" /syncfromflags:manual
w32tm /resync /force
```

## Build Instructions

To build the agent with time validation:
```bash
# Windows agent
env GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -tags agent,netgo -o incus-agent.exe ./cmd/incus-agent

# Linux agent (for testing)
go build -tags agent,netgo -o incus-agent-linux ./cmd/incus-agent
```