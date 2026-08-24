package util

import (
	"strings"

	"github.com/lxc/incus/v7/shared/uefi"
)

// ESLGUIDVar gets the GUID and variable name associated to the given ESL.
func ESLGUIDVar(esl string) (string, string) {
	var guid, varName string
	switch esl {
	case "pk", "kek":
		guid = uefi.EfiGlobalVariableGuid
		varName = strings.ToUpper(esl)
	case "db", "dbx", "dbt":
		guid = uefi.EfiImageSecurityDatabaseGuid
		varName = esl
	case "mok":
		guid = uefi.ShimLockGuid
		varName = "MokList"
	}

	return guid, varName
}
