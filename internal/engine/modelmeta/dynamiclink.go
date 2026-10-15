package modelmeta

import (
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// ValidateDynamicLink keeps expanded objects from overwriting stored field values.
func ValidateDynamicLink(md model.ModelDeclaration, f model.NamedField) error {
	if !strings.HasSuffix(f.Name, "_id") || f.Name == "_id" {
		return fmt.Errorf("DynamicLink field name must end in _id with a non-empty stem")
	}

	readKey := ReadKey(f.Name, f.Def.Kind.String())
	for _, other := range md.Fields {
		if other.Name != f.Name && (other.Name == readKey || ReadKey(other.Name, other.Def.Kind.String()) == readKey) {
			return fmt.Errorf("expanded read key %q conflicts with a declared field", readKey)
		}
	}
	return nil
}
