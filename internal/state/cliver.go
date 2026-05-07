// Package state — CLI version coupling.
//
// internal/state cannot import cmd/ (would create an import cycle).
// Instead, cmd/root.go's init() writes its build-time version into this
// package-level variable so the migration layer can record which CLI
// version applied each migration.
package state

// CLIVersion records the running binary version. Set by cmd/root.go init.
// Default "dev" is used in unit tests and for `go run` builds without ldflags.
var CLIVersion = "dev"
