package model

type DataMigration struct {
	FromVersion string `msgpack:"from_version"`
	ToVersion   string `msgpack:"to_version"`
	Description string `msgpack:"description,omitempty"`
	Handler     string `msgpack:"handler"`
}
