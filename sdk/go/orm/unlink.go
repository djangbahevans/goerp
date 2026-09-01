package orm

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

type ormUnlinkInput struct {
	Model string `msgpack:"model"`
	ID    string `msgpack:"id"`
}

type ormUnlinkOutput struct {
	Deleted bool `msgpack:"deleted"`
}

// Unlink deletes one record by ID via host.orm.unlink. Single-ID, unlike
// go-sdk-reference.md §6a's own documented bulk signature —
// host.orm.unlink doesn't support bulk delete yet (goerp#543).
func Unlink(model, id string) (bool, error) {
	var out ormUnlinkOutput
	err := hostcall.Do(hostORMUnlink, ormUnlinkInput{Model: model, ID: id}, &out)
	return out.Deleted, err
}
