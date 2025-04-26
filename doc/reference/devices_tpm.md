(devices-tpm)=
# Type: `tpm`

```{note}
The `tpm` device type is supported for both containers and VMs.
It supports hotplugging only for containers, not for VMs.
```

TPM devices enable access to a {abbr}`TPM (Trusted Platform Module)` emulator.

TPM devices can be used to validate the boot process and ensure that no steps in the boot chain have been tampered with, and they can securely generate and store encryption keys.

Incus uses a software TPM that supports TPM 2.0.
For containers, the main use case is sealing certificates, which means that the keys are stored outside of the container, making it virtually impossible for attackers to retrieve them.
For virtual machines, TPM can be used both for sealing certificates and for validating the boot process, which allows using full disk encryption compatible with, for example, Windows BitLocker.

## Device options

`tpm` devices have the following device options:

% Include content from [config_options.txt](../config_options.txt)
```{include} ../config_options.txt
    :start-after: <!-- config group devices-tpm start -->
    :end-before: <!-- config group devices-tpm end -->
```
