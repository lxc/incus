package uefi

import (
	"strings"

	"github.com/google/uuid"
)

const (
	// EDK2 Global.

	EdkiiVarErrorFlagGuid                    = "04b37fe8-f6ae-480b-bdd5-37d98c5e89aa"
	EfiAuthenticatedVariableGuid             = "aaf32c78-947b-439a-a180-2e144ec37792"
	EfiCertDbGuid                            = "d9bee56e-75dc-49d9-b4d7-b534210f637a"
	EfiCertPkcs7Guid                         = "4aafd29d-68df-49ee-8aa9-347d375665a7"
	EfiCertRsa2048Guid                       = "3c5766e8-269c-4e34-aa14-ed776e85b3b6"
	EfiCertRsa2048Sha1Guid                   = "67f8444f-8743-48f1-a328-1eaab8736080"
	EfiCertRsa2048Sha256Guid                 = "e2b36190-879b-4a3d-ad8d-f2e7bba32784"
	EfiCertSha1Guid                          = "826ca512-cf10-4ac9-b187-be01496631bd"
	EfiCertSha224Guid                        = "0b6e5233-a65c-44c9-9407-d9ab83bfc8bd"
	EfiCertSha256Guid                        = "c1c41626-504c-4092-aca9-41f936934328"
	EfiCertSha384Guid                        = "ff3e5307-9fd0-48c9-85f1-8ad56c701e01"
	EfiCertSha512Guid                        = "093e0fae-a6c4-4f50-9f1b-d41e2b89c19a"
	EfiCertSm3Guid                           = "57347f87-7a9b-403a-b93c-dc4afb7a0ebc"
	EfiCertX509Guid                          = "a5c059a1-94e4-4aa7-87b5-ab155c2bf072"
	EfiCertX509Sha256Guid                    = "3bd2a492-96c0-4079-b420-fcf98ef103ed"
	EfiCertX509Sha384Guid                    = "7076876e-80c2-4ee6-aad2-28b349a6865b"
	EfiCertX509Sha512Guid                    = "446dbf63-2502-4cda-bcfa-2465d2b0fe9d"
	EfiCertX509Sm3Guid                       = "60d807e5-10b4-49a9-9331-e40437888d37"
	EfiCustomModeEnableGuid                  = "c076ec0c-7028-4399-a072-71ee5c448b9f"
	EfiDebugPortProtocolGuid                 = "eba4e8d2-3858-41ec-a281-2647ba9660d0"
	EfiDhcp6ServiceBindingProtocolGuid       = "9fb9a8a1-2f4a-43a6-889c-d0f7b6c47ad5"
	EfiGlobalVariableGuid                    = "8be4df61-93ca-11d2-aa0d-00e098032b8c"
	EfiImageSecurityDatabaseGuid             = "d719b2cb-3d3a-4596-a3bc-dad00e67656f"
	EfiIp4Config2ProtocolGuid                = "5b446ed1-e30b-4faa-871a-3654eca36080"
	EfiIp6ConfigProtocolGuid                 = "937fe521-95ae-4d1a-8929-48bcd90ad31a"
	EfiMemoryOverwriteControlDataGuid        = "e20939be-32d4-41be-a150-897f85d49829"
	EfiMemoryOverwriteRequestControlLockGuid = "bb983ccf-151d-40e1-a07b-4a17be168292"
	EfiMemoryTypeInformationGuid             = "4c19049f-4137-4dd3-9c10-8b97a83ffdfa"
	EfiPcAnsiGuid                            = "e0c14753-f9be-11d2-9a0c-0090273fc14d"
	EfiPersistentVirtualCdGuid               = "08018188-42cd-bb48-100f-5387d53ded3d"
	EfiPersistentVirtualDiskGuid             = "5cea02c9-4d07-69d3-269f-4496fbe096f9"
	EfiSasDevicePathGuid                     = "d487ddb4-008b-11d9-afdc-001083ffca4d"
	EfiSecureBootEnableDisableGuid           = "f0a30bc7-af08-4556-99c4-001009c93a44"
	EfiSystemNvDataFvGuid                    = "fff12b8d-7696-4c8b-a985-2747075b4f50"
	EfiUartDevicePathGuid                    = "37499a9d-542f-4c89-a026-35da142094e4"
	EfiVT100Guid                             = "dfa66065-b419-11d3-9a2d-0090273fc14d"
	EfiVT100PlusGuid                         = "7baec70b-57e0-4c76-8e87-2f9e28088343"
	EfiVTUTF8Guid                            = "ad15a0d6-8bec-4acf-a073-d01de77e2d88"
	EfiVendorKeysNvGuid                      = "9073e4e0-60ec-4b6e-9903-4c223c260f3c"
	EfiVirtualCdGuid                         = "3d5abd30-4175-87ce-6d64-d2ade523c4bb"
	EfiVirtualDiskGuid                       = "77ab535a-45fc-624b-5560-f7b281d1f96e"
	IScsiConfigGuid                          = "4b47d616-a8d6-4552-9d44-ccad2e0f4cf9"
	MtcVendorGuid                            = "eb704011-1402-11d3-8e77-00a0c969723b"
	Tcg2ConfigFormSetGuid                    = "6339d487-26ba-424b-9a5d-687e25d740bc"

	// EDK2 Module-local.

	BmHardDriveBootVariableGuid = "fab7e9e1-39dd-4f2b-8408-e20e906cb6de"

	// External.

	ShimLockGuid = "605dab50-e046-4300-abb6-3dd810dd8b23"
)

var guidNames = map[string]string{
	// EDK2 Global.

	EdkiiVarErrorFlagGuid:                    "EDKII_VAR_ERROR_FLAG_GUID",
	EfiAuthenticatedVariableGuid:             "EFI_AUTHENTICATED_VARIABLE_GUID",
	EfiCertDbGuid:                            "EFI_CERT_DB_GUID",
	EfiCertPkcs7Guid:                         "EFI_CERT_PKCS7_GUID",
	EfiCertRsa2048Guid:                       "EFI_CERT_RSA2048_GUID",
	EfiCertRsa2048Sha1Guid:                   "EFI_CERT_RSA2048_SHA1_GUID",
	EfiCertRsa2048Sha256Guid:                 "EFI_CERT_RSA2048_SHA256_GUID",
	EfiCertSha1Guid:                          "EFI_CERT_SHA1_GUID",
	EfiCertSha224Guid:                        "EFI_CERT_SHA224_GUID",
	EfiCertSha256Guid:                        "EFI_CERT_SHA256_GUID",
	EfiCertSha384Guid:                        "EFI_CERT_SHA384_GUID",
	EfiCertSha512Guid:                        "EFI_CERT_SHA512_GUID",
	EfiCertSm3Guid:                           "EFI_CERT_SM3_GUID",
	EfiCertX509Guid:                          "EFI_CERT_X509_GUID",
	EfiCertX509Sha256Guid:                    "EFI_CERT_X509_SHA256_GUID",
	EfiCertX509Sha384Guid:                    "EFI_CERT_X509_SHA384_GUID",
	EfiCertX509Sha512Guid:                    "EFI_CERT_X509_SHA512_GUID",
	EfiCertX509Sm3Guid:                       "EFI_CERT_X509_SM3_GUID",
	EfiCustomModeEnableGuid:                  "EFI_CUSTOM_MODE_ENABLE_GUID",
	EfiDebugPortProtocolGuid:                 "EFI_DEBUGPORT_PROTOCOL_GUID",
	EfiDhcp6ServiceBindingProtocolGuid:       "EFI_DHCP6_SERVICE_BINDING_PROTOCOL_GUID",
	EfiGlobalVariableGuid:                    "EFI_GLOBAL_VARIABLE",
	EfiImageSecurityDatabaseGuid:             "EFI_IMAGE_SECURITY_DATABASE_GUID",
	EfiIp4Config2ProtocolGuid:                "EFI_IP4_CONFIG2_PROTOCOL_GUID",
	EfiIp6ConfigProtocolGuid:                 "EFI_IP6_CONFIG_PROTOCOL_GUID",
	EfiMemoryOverwriteControlDataGuid:        "MEMORY_ONLY_RESET_CONTROL_GUID",
	EfiMemoryOverwriteRequestControlLockGuid: "MEMORY_OVERWRITE_REQUEST_CONTROL_LOCK_GUID",
	EfiMemoryTypeInformationGuid:             "EFI_MEMORY_TYPE_INFORMATION_GUID",
	EfiPcAnsiGuid:                            "EFI_PC_ANSI_GUID",
	EfiPersistentVirtualCdGuid:               "EFI_PERSISTENT_VIRTUAL_CD_GUID",
	EfiPersistentVirtualDiskGuid:             "EFI_PERSISTENT_VIRTUAL_DISK_GUID",
	EfiSasDevicePathGuid:                     "EFI_SAS_DEVICE_PATH_GUID",
	EfiSecureBootEnableDisableGuid:           "EFI_SECURE_BOOT_ENABLE_DISABLE_GUID",
	EfiSystemNvDataFvGuid:                    "EFI_SYSTEM_NVDATA_FV_GUID",
	EfiUartDevicePathGuid:                    "EFI_UART_DEVICE_PATH_GUID",
	EfiVT100Guid:                             "EFI_VT_100_GUID",
	EfiVT100PlusGuid:                         "EFI_VT_100_PLUS_GUID",
	EfiVTUTF8Guid:                            "EFI_VT_UTF8_GUID",
	EfiVendorKeysNvGuid:                      "EFI_VENDOR_KEYS_NV_GUID",
	EfiVirtualCdGuid:                         "EFI_VIRTUAL_CD_GUID",
	EfiVirtualDiskGuid:                       "EFI_VIRTUAL_DISK_GUID",
	IScsiConfigGuid:                          "ISCSI_CONFIG_GUID",
	MtcVendorGuid:                            "MTC_VENDOR_GUID",
	Tcg2ConfigFormSetGuid:                    "TCG2_CONFIG_FORM_SET_GUID",

	// EDK2 Module-local.

	BmHardDriveBootVariableGuid: "HD_BOOT_DEVICE_PATH_VARIABLE_GUID",

	// External.

	ShimLockGuid: "SHIM_LOCK_GUID",
}

// We pre-initialize the aliases with irregular values.
var guidAliases = map[string]string{
	"efimemoryoverwritecontroldata":        EfiMemoryOverwriteControlDataGuid,
	"efimemoryoverwriterequestcontrollock": EfiMemoryOverwriteRequestControlLockGuid,
	"bmharddrivebootvariable":              BmHardDriveBootVariableGuid,
}

// normalizeGUIDName transforms a GUID name into something easier for us to handle and more
// forgiving to the users.
func normalizeGUIDName(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	s = strings.TrimSuffix(s, "guid")
	return s
}

// init initializes the aliases slice.
func init() {
	for g, name := range guidNames {
		guidAliases[normalizeGUIDName(name)] = g
	}
}

// GUIDName returns a familiar symbolic name for a known GUID, or the GUID itself.
func GUIDName(g string) string {
	name, ok := guidNames[g]
	if ok {
		return name
	}

	return g
}

// ParseGUID tries to parse the given string as a GUID.
func ParseGUID(s string) (string, error) {
	guid, err := uuid.Parse(s)
	if err != nil {
		return "", err
	}

	return guid.String(), nil
}

// ParseGUIDOrName tries to parse the given string as a familiar GUID name, or as a raw GUID.
func ParseGUIDOrName(s string) (string, error) {
	guid, ok := guidAliases[normalizeGUIDName(s)]
	if ok {
		return guid, nil
	}

	return ParseGUID(s)
}
