package api

// SecretNameControl is the secret name used for the migration control connection.
const SecretNameControl = "control"

// SecretNameFilesystem is the secret name used for the migration filesystem connection.
const SecretNameFilesystem = "fs"

// SecretNameState is the secret name used for the migration state connection.
const SecretNameState = "criu" // Legacy value used for backward compatibility for clients.
