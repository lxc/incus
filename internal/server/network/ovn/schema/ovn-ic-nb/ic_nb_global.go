// Code generated by "libovsdb.modelgen"
// DO NOT EDIT.

package ovsmodel

const ICNBGlobalTable = "IC_NB_Global"

// ICNBGlobal defines an object in IC_NB_Global table
type ICNBGlobal struct {
	UUID        string            `ovsdb:"_uuid"`
	Connections []string          `ovsdb:"connections"`
	ExternalIDs map[string]string `ovsdb:"external_ids"`
	Options     map[string]string `ovsdb:"options"`
	SSL         *string           `ovsdb:"ssl"`
}
