package uefi

import (
	"encoding/json"

	"github.com/lxc/incus/v7/shared/api"
)

func getDissector(guid string, name string) dissector {
	switch guid {
	case BmHardDriveBootVariableGuid:
		switch name {
		case "HDDP":
			return devicePath
		}

	case EdkiiVarErrorFlagGuid:
		switch name {
		case "VarErrorFlag":
			return errorFlag
		}

	case EfiCertDbGuid:
		switch name {
		case "certdb":
			return certDB
		}

	case EfiCustomModeEnableGuid:
		switch name {
		case "CustomMode":
			return b8
		}

	case EfiGlobalVariableGuid:
		bootPrefix, _, ok := ParseBootXXXX(name)
		if ok {
			switch bootPrefix {
			case "Boot", "Driver", "SysPrep", "OsRecovery", "PlatformRecovery":
				return boot
			case "Key":
				return keyboard
			}
		}

		switch name {
		case "BootNext":
			return bootNext
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

	case EfiImageSecurityDatabaseGuid:
		switch name {
		case "db", "dbr", "dbt", "dbx":
			return esl
		}

	case EfiMemoryOverwriteControlDataGuid:
		switch name {
		case "MemoryOverwriteRequestControl":
			return morControl
		}

	case EfiMemoryOverwriteRequestControlLockGuid:
		switch name {
		case "MemoryOverwriteRequestControlLock":
			return morControlLock
		}

	case EfiSecureBootEnableDisableGuid:
		switch name {
		case "SecureBootEnable":
			return b8
		}

	case EfiVendorKeysNvGuid:
		switch name {
		case "VendorKeysNv":
			return b8
		}

	case IScsiConfigGuid:
		switch name {
		case "InitialAttemptOrder":
			return attemptOrder
		}

	case MtcVendorGuid:
		switch name {
		case "MTC":
			return u32
		}

	case OvmfPlatformConfigGuid:
		switch name {
		case "PlatformConfig":
			return platformConfig
		}

	case ShimLockGuid:
		switch name {
		case "MokList":
			return esl
		case "SbatLevel":
			return z8
		}

	case Tcg2ConfigFormSetGuid:
		switch name {
		case "TCG2_CONFIGURATION", "TCG2_DEVICE_DETECTION":
			return tpmVersion
		case "TCG2_VERSION":
			return tcg2Version
		}
	}

	return dissector{
		dissect: func([]byte) (any, error) { return nil, errUnexpectedData },
		format:  func(json.RawMessage) ([]byte, error) { return nil, errUnexpectedData },
	}
}

// Dissect dissects a UEFI variable.
func Dissect(v *api.InstanceNVRAMVariable, guid string, name string) error {
	dissected, err := getDissector(guid, name).dissect(v.Binary)
	if err == nil {
		v.Data = dissected
	}

	return nil
}

// Format formats a UEFI variable.
func Format(v *api.InstanceNVRAMVariable, guid string, name string) error {
	raw, ok := v.Data.(json.RawMessage)
	if !ok {
		var err error
		raw, err = json.Marshal(v.Data)
		if err != nil {
			return err
		}
	}

	formatted, err := getDissector(guid, name).format(raw)
	if err == nil {
		v.Binary = formatted
	}

	return err
}
