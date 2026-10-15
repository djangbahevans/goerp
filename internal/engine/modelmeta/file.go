package modelmeta

import (
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func ValidateFile(md model.ModelDeclaration, f model.NamedField) error {
	if md.Backend != "" {
		return fmt.Errorf("file references require a Postgres-backed model")
	}

	if !strings.HasSuffix(f.Name, "_id") || f.Name == "_id" {
		return fmt.Errorf("file field name must end in _id with a non-empty stem")
	}

	if f.Def.RelatedModel != "" || f.Def.RelationDomain != "" || f.Def.IsTree || f.Def.RelationOnDelete != model.Restrict {
		return fmt.Errorf("file fields reference tenant files and cannot declare a model target, domain, tree or delete action")
	}

	key := ReadKey(f.Name, f.Def.Kind.String())
	for _, other := range md.Fields {
		if other.Name != f.Name && (other.Name == key || ReadKey(other.Name, other.Def.Kind.String()) == key) {
			return fmt.Errorf("expanded read key %q conflicts with a declared field", key)
		}
	}

	return nil
}
