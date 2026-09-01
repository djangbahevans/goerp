package orm

import "github.com/djangbahevans/goerp/sdk/go/internal/hostcall"

type ormUnlinkInput struct {
	Model string   `msgpack:"model"`
	IDs   []string `msgpack:"ids"`
}

// Unlink deletes records by ID via host.orm.unlink. A missing ID aborts
// the whole call — matching WriteMany/WriteWhere's own all-or-nothing
// semantics — so a returned ExecResult always has Count == len(ids).
func Unlink(model string, ids []string) (ExecResult, error) {
	var out ExecResult
	err := hostcall.Do(hostORMUnlink, ormUnlinkInput{Model: model, IDs: ids}, &out)
	return out, err
}
