package uefi

import (
	"github.com/lxc/incus/v7/shared/api"
)

// Dissect dissects an UEFI variable.
func Dissect(v *api.InstanceNVRAMVariable, guid string, name string) error {
	dissected, err := func() (any, error) {
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
					return boot(v.Binary)
				case "Key":
					return keyboard(v.Binary)
				}
			}

			switch name {
			case "BootOrder":
				return bootOrder("Boot", v.Binary)
			case "ConIn", "ConOut", "ErrOut":
				return devicePath(v.Binary)
			case "DriverOrder":
				return bootOrder("Driver", v.Binary)
			case "KEK", "PK":
				return esl(v.Binary)
			case "Lang", "PlatformLang":
				return zn8(v.Binary)
			case "OsIndications":
				return osIndications(v.Binary)
			case "Timeout":
				return u16(v.Binary)
			}

		case ShimLockGuid:
			switch name {
			case "MokList":
				return esl(v.Binary)
			case "SbatLevel":
				return z8(v.Binary)
			}

		case EfiVendorKeysNvGuid:
			switch name {
			case "VendorKeysNv":
				return b8(v.Binary)
			}

		case EfiCustomModeEnableGuid:
			switch name {
			case "CustomMode":
				return b8(v.Binary)
			}

		case MtcVendorGuid:
			switch name {
			case "MTC":
				return u32(v.Binary)
			}

		case EfiSecureBootEnableDisableGuid:
			switch name {
			case "SecureBootEnable":
				return b8(v.Binary)
			}

		case EfiImageSecurityDatabaseGuid:
			switch name {
			case "db", "dbr", "dbt", "dbx":
				return esl(v.Binary)
			}

		case EdkiiVarErrorFlagGuid:
			switch name {
			case "VarErrorFlag":
				return errorFlag(v.Binary)
			}

		case IScsiConfigGuid:
			switch name {
			case "InitialAttemptOrder":
				return attemptOrder(v.Binary)
			}

		case Tcg2ConfigFormSetGuid:
			switch name {
			case "TCG2_CONFIGURATION", "TCG2_DEVICE_DETECTION":
				return tpmVersion(v.Binary)
			case "TCG2_VERSION":
				return tcg2Version(v.Binary)
			}

		case EfiMemoryOverwriteRequestControlLockGuid:
			switch name {
			case "MemoryOverwriteRequestControlLock":
				return morControlLock(v.Binary)
			}

		case EfiMemoryOverwriteControlDataGuid:
			switch name {
			case "MemoryOverwriteRequestControl":
				return morControl(v.Binary)
			}

		case BmHardDriveBootVariableGuid:
			switch name {
			case "HDDP":
				return devicePath(v.Binary)
			}

		case EfiCertDbGuid:
			switch name {
			case "certdb":
				return certDB(v.Binary)
			}
		}

		return nil, errUnexpectedData
	}()
	if err == nil {
		v.Data = dissected
	}

	return nil
}
