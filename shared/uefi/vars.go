package uefi

import (
	"encoding/json"
	"fmt"

	"github.com/lxc/incus/v7/shared/api"
)

func getDissector(guid string, name string) dissector {
	switch guid {
	case EfiGlobalVariableGuid:
		hasFourDigits := false
		n := len(name)
		if n > 4 {
			hasFourDigits = true
			for _, c := range name[n-4:] {
				if c < '0' || c > '9' {
					hasFourDigits = false
					break
				}
			}
		}

		if hasFourDigits {
			switch name[:n-4] {
			case "Boot", "Driver", "SysPrep", "OsRecovery", "PlatformRecovery":
				return boot
			case "Key":
				return keyboard
			}
		}

		switch name {
		case "BootOrder":
			return bootOrder("Boot")
		case "ConIn", "ConOut", "ErrOut":
			return devicePath
		case "DriverOrder":
			return bootOrder("Driver")
		case "KEK", "PK":
			return esl
		case "Lang", "PlatformLang":
			return zn8
		case "OsIndications":
			return osIndications
		case "Timeout":
			return u16
		}

	case ShimLockGuid:
		switch name {
		case "MokList":
			return esl
		case "SbatLevel":
			return z8
		}

	case EfiVendorKeysNvGuid:
		switch name {
		case "VendorKeysNv":
			return b8
		}

	case EfiCustomModeEnableGuid:
		switch name {
		case "CustomMode":
			return b8
		}

	case MtcVendorGuid:
		switch name {
		case "MTC":
			return u32
		}

	case EfiSecureBootEnableDisableGuid:
		switch name {
		case "SecureBootEnable":
			return b8
		}

	case EfiImageSecurityDatabaseGuid:
		switch name {
		case "db", "dbr", "dbt", "dbx":
			return esl
		}

	case EdkiiVarErrorFlagGuid:
		switch name {
		case "VarErrorFlag":
			return errorFlag
		}

	case IScsiConfigGuid:
		switch name {
		case "InitialAttemptOrder":
			return attemptOrder
		}

	case Tcg2ConfigFormSetGuid:
		switch name {
		case "TCG2_CONFIGURATION", "TCG2_DEVICE_DETECTION":
			return tpmVersion
		case "TCG2_VERSION":
			return tcg2Version
		}

	case EfiMemoryOverwriteRequestControlLockGuid:
		switch name {
		case "MemoryOverwriteRequestControlLock":
			return morControlLock
		}

	case EfiMemoryOverwriteControlDataGuid:
		switch name {
		case "MemoryOverwriteRequestControl":
			return morControl
		}

	case BmHardDriveBootVariableGuid:
		switch name {
		case "HDDP":
			return devicePath
		}

	case EfiCertDbGuid:
		switch name {
		case "certdb":
			return certDB
		}
	}

	return dissector{
		dissect: func([]byte) (any, error) { return nil, errUnexpectedData },
		format:  func(json.RawMessage) ([]byte, error) { return nil, errUnexpectedData },
	}
}

// Dissect dissects an UEFI variable.
func Dissect(v *api.InstanceNVRAMVariable, guid string, name string) error {
	dissected, err := getDissector(guid, name).dissect(v.Binary)
	if err == nil {
		v.Data = dissected
	}

	return nil
}

// Format formats an UEFI variable.
func Format(v *api.InstanceNVRAMVariable, guid string, name string) error {
	raw, ok := v.Data.(json.RawMessage)
	if !ok {
		return fmt.Errorf("Unexpected type %T", v.Data)
	}

	formatted, err := getDissector(guid, name).format(raw)
	if err == nil {
		v.Binary = formatted
	}

	return err
}
