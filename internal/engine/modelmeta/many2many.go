package modelmeta

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/dbscope"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

var relationIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// ValidateMany2Many checks the declaration independently of a tenant's live schema.
func ValidateMany2Many(md model.ModelDeclaration, f model.NamedField) error {
	d := f.Def
	if !strings.HasSuffix(f.Name, "_ids") || f.Name == "_ids" {
		return fmt.Errorf("Many2Many field name must end in _ids")
	}

	for _, name := range []string{f.Name, d.JoinTable, d.Column1, d.Column2} {
		if !relationIdentifier.MatchString(name) {
			return fmt.Errorf("invalid Many2Many identifier %q", name)
		}
	}
	if dbscope.IsReservedTableName(d.JoinTable) {
		return fmt.Errorf("join table name %q is reserved", d.JoinTable)
	}
	if d.Column1 == d.Column2 {
		return fmt.Errorf("join columns must have distinct names")
	}

	readKey := ReadKey(f.Name, d.Kind.String())
	for _, other := range md.Fields {
		if other.Name != f.Name && (other.Name == readKey || ReadKey(other.Name, other.Def.Kind.String()) == readKey) {
			return fmt.Errorf("expanded read key %q conflicts with a declared field", readKey)
		}
	}

	if d.IsPrimaryKey || d.IsPrimary || d.IsComputed || d.DefaultExpr != nil || d.IsTree || d.IsTracked {
		return fmt.Errorf("Many2Many cannot declare column-only modifiers")
	}
	if d.IsRequired || d.RelationDomain != "" || d.RelationOnDelete != model.Restrict {
		return fmt.Errorf("Many2Many does not support Required, Domain or OnDelete")
	}
	return ValidateMany2ManyTarget(md)
}

// ValidateMany2ManyTarget requires a persistent table with one UUID key.
func ValidateMany2ManyTarget(md model.ModelDeclaration) error {
	if md.Backend != "" {
		return fmt.Errorf("Many2Many model %q requires a Postgres table", md.Name)
	}

	keys := 0
	for _, field := range md.Fields {
		if field.Def.IsPrimaryKey {
			keys++
			if field.Def.Kind != model.KindUUID {
				return fmt.Errorf("Many2Many model %q requires a UUID primary key", md.Name)
			}
		}
	}
	if keys != 1 {
		return fmt.Errorf("Many2Many model %q requires a single UUID primary key", md.Name)
	}
	return nil
}
