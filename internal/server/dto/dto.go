// Package dto defines the wire contracts for MCP tool responses.
//
// Every type in this package is a deliberate copy of the response shape —
// decoupled from internal engine types so that renaming an internal struct
// or changing its json tags can no longer silently break MCP clients.
// Handlers map engine results into these DTOs; the json tags here are the
// frozen contract, locked by golden-file tests in testdata/.
//
// Every top-level response embeds Contract, which stamps schema_version so
// clients can feature-detect contract changes.
package dto

// SchemaVersion is the current response contract version. Bump when a
// breaking change is made to any DTO's wire shape; clients can branch on
// schema_version to migrate.
const SchemaVersion = 1

// Contract is embedded in every top-level response DTO and flattens to a
// top-level schema_version field in the marshaled JSON.
type Contract struct {
	SchemaVersion int `json:"schema_version"`
}
