package module

import (
	"bytes"
	"fmt"
	"go/format"
	"regexp"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// commonInitialisms is golint's own commonInitialisms list — the
// initialism convention go-sdk-reference.md §22/goerp#961 commits
// generated field and type names to (id -> ID, url -> URL, not
// Id/Url).
var commonInitialisms = map[string]bool{
	"ACL": true, "API": true, "ASCII": true, "CPU": true, "CSS": true,
	"DNS": true, "EOF": true, "GUID": true, "HTML": true, "HTTP": true,
	"HTTPS": true, "ID": true, "IP": true, "JSON": true, "LHS": true,
	"QPS": true, "RAM": true, "RHS": true, "RPC": true, "SLA": true,
	"SMTP": true, "SQL": true, "SSH": true, "TCP": true, "TLS": true,
	"TTL": true, "UDP": true, "UI": true, "UID": true, "UUID": true,
	"URI": true, "URL": true, "UTF8": true, "VM": true, "XML": true,
	"XMPP": true, "XSRF": true, "XSS": true,
}

// identifierSplit finds the run boundaries pascalCase splits an input
// on — not just "_": a Selection/Enum value can carry any non-identifier
// separator ("in-progress", "needs review"), and leaving it unsplit
// would land straight into a generated Go identifier, producing source
// go/format.Source can't parse.
var identifierSplit = regexp.MustCompile(`[^A-Za-z0-9]+`)

// pascalCase converts a snake_case (or otherwise separator-delimited)
// identifier — a field name, or a Selection/Enum value — into the
// PascalCase Go identifier go-sdk-reference.md §22/goerp#961 generates
// field and type names as: each segment is either an initialism
// (commonInitialisms, upper-cased whole) or Title-cased.
func pascalCase(s string) string {
	var b strings.Builder
	for _, part := range identifierSplit.Split(s, -1) {
		if part == "" {
			continue
		}
		if upper := strings.ToUpper(part); commonInitialisms[upper] {
			b.WriteString(upper)
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

// renderModelFile writes m's generated struct — go-sdk-reference.md §22
// "Package layout" and this repo's own docs/design-902-module-generate.md
// §3 are the canonical shape reference. types is schema.Schema's own
// Types slice, needed to resolve an Enum field's declared values.
func renderModelFile(m *model.ModelDeclaration, types []model.TypeDeclaration) ([]byte, error) {
	structName := pascalCase(m.ResourceName())
	if structName == "" {
		return nil, fmt.Errorf("model %q has no usable resource name to generate a struct from", m.Name)
	}

	// siblingRequired marks every Selection field a DynamicLink field
	// names via ReferenceTypeField (go-sdk-reference.md §22
	// "DynamicLink" points at an existing sibling rather than declaring
	// its own allowlist) — skipped when the field loop reaches it on
	// its own, and emitted once, alongside its DynamicLink partner,
	// however many DynamicLink fields happen to name the same sibling
	// (a plausible shape: two DynamicLink ends of one polymorphic link
	// sharing one Selection field for their target-model choice).
	fieldByName := make(map[string]model.NamedField, len(m.Fields))
	siblingOfDynamicLink := map[string]bool{}
	for _, f := range m.Fields {
		fieldByName[f.Name] = f
		if f.Def.Kind == model.KindDynamicLink {
			siblingOfDynamicLink[f.Def.ReferenceTypeField] = true
		}
	}

	var body, aux bytes.Buffer // aux: Selection/Enum named types + constants
	usesTime, usesOrm := false, false
	siblingEmitted := map[string]bool{}

	// usedFieldNames catches two fields whose generated Go names
	// collide — go/format.Source only parses and formats, it doesn't
	// type-check, so a struct with two identically-named fields (the
	// most plausible trigger: a Many2One field's stripped `_id`
	// expansion name landing on an unrelated sibling field's own name)
	// would otherwise pass straight through as gofmt-clean, non-
	// compiling output (goerp#970).
	usedFieldNames := map[string]string{}
	claimFieldName := func(goName, source string) error {
		if prior, ok := usedFieldNames[goName]; ok {
			return fmt.Errorf("%s and %s both generate the Go field name %q — rename one", prior, source, goName)
		}
		usedFieldNames[goName] = source
		return nil
	}

	for _, f := range m.Fields {
		if f.Def.Kind == model.KindOne2Many {
			continue // no backing column (go-sdk-reference.md §22 "One2Many")
		}
		if siblingOfDynamicLink[f.Name] {
			continue // generated alongside its DynamicLink partner(s) below
		}

		switch f.Def.Kind {
		case model.KindDynamicLink:
			siblingName := f.Def.ReferenceTypeField
			sibling, ok := fieldByName[siblingName]
			if !ok {
				return nil, fmt.Errorf("field %q: DynamicLink names an unknown sibling field %q", f.Name, siblingName)
			}
			if !siblingEmitted[siblingName] {
				siblingGoName := pascalCase(sibling.Name)
				if err := claimFieldName(siblingGoName, fmt.Sprintf("field %q", sibling.Name)); err != nil {
					return nil, err
				}
				writeField(&body, siblingGoName, sibling.Name, "string", sibling.Def.IsRequired)
				siblingEmitted[siblingName] = true
			}
			selfGoName := pascalCase(f.Name)
			if err := claimFieldName(selfGoName, fmt.Sprintf("field %q", f.Name)); err != nil {
				return nil, err
			}
			writeField(&body, selfGoName, f.Name, "string", f.Def.IsRequired)

		case model.KindMany2One:
			if !strings.HasSuffix(f.Name, "_id") {
				return nil, fmt.Errorf("field %q: a Many2One field's name must end in \"_id\" (go-sdk-reference.md §22 \"Many2One\")", f.Name)
			}
			fkGoName := pascalCase(f.Name)
			if err := claimFieldName(fkGoName, fmt.Sprintf("field %q", f.Name)); err != nil {
				return nil, err
			}
			writeField(&body, fkGoName, f.Name, "string", f.Def.IsRequired || f.Def.IsPrimaryKey)

			expansionName := strings.TrimSuffix(f.Name, "_id")
			expansionGoName := pascalCase(expansionName)
			if err := claimFieldName(expansionGoName, fmt.Sprintf("field %q's Many2One expansion %q", f.Name, expansionName)); err != nil {
				return nil, err
			}
			fmt.Fprintf(&body, "\t%s *orm.RelationRef `db:%q`\n", expansionGoName, expansionName)
			usesOrm = true

		case model.KindSelection:
			fieldGoName := pascalCase(f.Name)
			if err := claimFieldName(fieldGoName, fmt.Sprintf("field %q", f.Name)); err != nil {
				return nil, err
			}
			if err := writeNamedTypeField(&body, &aux, structName, f.Name, fieldGoName, f.Def.SelectionValues, f.Def.IsRequired || f.Def.IsPrimaryKey); err != nil {
				return nil, fmt.Errorf("field %q: %w", f.Name, err)
			}

		case model.KindEnum:
			values, err := enumValues(types, f.Def.EnumType)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", f.Name, err)
			}
			fieldGoName := pascalCase(f.Name)
			if err := claimFieldName(fieldGoName, fmt.Sprintf("field %q", f.Name)); err != nil {
				return nil, err
			}
			if err := writeNamedTypeField(&body, &aux, structName, f.Name, fieldGoName, values, f.Def.IsRequired || f.Def.IsPrimaryKey); err != nil {
				return nil, fmt.Errorf("field %q: %w", f.Name, err)
			}

		default:
			goType, err := baseGoType(f.Def.Kind)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", f.Name, err)
			}
			fieldGoName := pascalCase(f.Name)
			if err := claimFieldName(fieldGoName, fmt.Sprintf("field %q", f.Name)); err != nil {
				return nil, err
			}
			if goType == "time.Time" {
				usesTime = true
			}
			writeField(&body, fieldGoName, f.Name, goType, f.Def.IsRequired || f.Def.IsPrimaryKey)
		}
	}

	var buf bytes.Buffer
	buf.WriteString(generatedFileHeader)
	buf.WriteString("package models\n\n")
	if usesTime || usesOrm {
		buf.WriteString("import (\n")
		if usesTime {
			buf.WriteString("\t\"time\"\n")
		}
		if usesOrm {
			if usesTime {
				buf.WriteString("\n")
			}
			buf.WriteString("\t\"github.com/djangbahevans/goerp/sdk/go/orm\"\n")
		}
		buf.WriteString(")\n\n")
	}
	fmt.Fprintf(&buf, "// %s corresponds to model %q.\n", structName, m.Name)
	fmt.Fprintf(&buf, "type %s struct {\n", structName)
	buf.Write(body.Bytes())
	buf.WriteString("}\n")
	if aux.Len() > 0 {
		buf.WriteString("\n")
		buf.Write(aux.Bytes())
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, err
	}
	return formatted, nil
}

// writeField renders one struct field line: goName (already PascalCase
// — the caller's own claimFieldName call already computed it, so this
// doesn't recompute it and risk the claimed name and the emitted name
// silently diverging), goType (pointer-prefixed unless required), and
// an explicit db tag using fieldName verbatim — never left to orm's own
// snake_case fallback (sdk/go/orm/reflect.go's ormFields).
func writeField(buf *bytes.Buffer, goName, fieldName, goType string, required bool) {
	if !required {
		goType = "*" + goType
	}
	fmt.Fprintf(buf, "\t%s %s `db:%q`\n", goName, goType, fieldName)
}

// baseGoType is the field-kind -> Go type table goerp#960 empirically
// determined (Decimal/Time as string, TimestampTZ/Date as time.Time,
// JSONB/Bytea as []byte) alongside the kinds that were already proven by
// existing usage. Many2One, Selection, Enum, DynamicLink, and One2Many
// each have their own generation shape and never reach this function.
func baseGoType(k model.FieldKind) (string, error) {
	switch k {
	case model.KindInteger:
		return "int32", nil
	case model.KindBigInt:
		return "int64", nil
	case model.KindFloat:
		return "float64", nil
	case model.KindBoolean:
		return "bool", nil
	case model.KindUUID, model.KindChar, model.KindText, model.KindSequence, model.KindDecimal, model.KindTime:
		return "string", nil
	case model.KindTimestampTZ, model.KindDate:
		return "time.Time", nil
	case model.KindJSONB, model.KindBytea:
		return "[]byte", nil
	default:
		return "", fmt.Errorf("unsupported field kind %s", k)
	}
}

// enumValues resolves an Enum field's declared values from the schema's
// own Types slice (model.EnumType(name, values...) in schema/schema.go)
// — not a Postgres read, go-sdk-reference.md §22 "Enum types".
func enumValues(types []model.TypeDeclaration, enumType string) ([]string, error) {
	for _, t := range types {
		if t.Name == enumType {
			return t.Values, nil
		}
	}
	return nil, fmt.Errorf("enum type %q not declared in schema.Types", enumType)
}

// writeNamedTypeField is Selection and KindEnum's shared generation
// shape: a named string type plus one constant per value —
// go-sdk-reference.md §22/goerp#961's "<Struct><Field>" type name and
// "<Type><Value>" constant name convention, each value run through the
// same pascalCase pass as a field name — plus the struct field itself,
// typed as that named type.
func writeNamedTypeField(body, aux *bytes.Buffer, structName, fieldName, fieldGoName string, values []string, required bool) error {
	if fieldGoName == "" {
		return fmt.Errorf("no usable Go identifier for field name %q", fieldName)
	}
	typeName := structName + fieldGoName

	fmt.Fprintf(aux, "type %s string\n\n", typeName)
	aux.WriteString("const (\n")
	for _, v := range values {
		fmt.Fprintf(aux, "\t%s%s %s = %q\n", typeName, pascalCase(v), typeName, v)
	}
	aux.WriteString(")\n\n")

	goType := typeName
	if !required {
		goType = "*" + goType
	}
	fmt.Fprintf(body, "\t%s %s `db:%q`\n", fieldGoName, goType, fieldName)
	return nil
}
