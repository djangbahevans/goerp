// Package orm reads and writes records through the engine's host.orm
// calls. It is the default data-access path for a module: the engine
// applies ABAC rules, field security, computed fields and constraints to
// every call automatically.
package orm
