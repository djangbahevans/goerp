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

// renderModelFile writes m's generated struct, ResourceName() method,
// field descriptors (<Struct>Fields, <Struct>AllFields), and Scan method
// — go-sdk-reference.md §22 "Package layout", §26 "Complete module
// example", and this repo's own docs/design-902-module-generate.md §3
// are the canonical shape reference for the struct itself; goerp#973 is
// the Model/Field/StringField/OrderedField/TimeField/BytesField family
// the descriptors bind to; goerp#974 is the Scan/DecodeError contract
// Scan implements against. types is schema.Schema's own Types slice,
// needed to resolve an Enum field's declared values.
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

	// body/aux: the struct itself and its Selection/Enum named types +
	// constants (unchanged from before goerp#977). fieldsDecl/fieldsInit:
	// <Struct>Fields' own anonymous struct type and literal. allFields:
	// <Struct>AllFields' slice literal entries. scanBody: Scan's own
	// per-field decode blocks.
	var body, aux, fieldsDecl, fieldsInit, allFields, scanBody bytes.Buffer
	usesTime := false
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

	// emitScalarField renders goName's struct field line, field-
	// descriptor entry, AllFields entry, and Scan block together — the
	// one place every Condition-bearing field (the default switch case,
	// Many2One's FK column, and DynamicLink's two string fields) keeps
	// its four generated pieces in sync.
	emitScalarField := func(goName, fieldName, goType, wrapper string, required bool) {
		writeField(&body, goName, fieldName, goType, required)
		writeFieldDescriptor(&fieldsDecl, &fieldsInit, &allFields, structName, goName, fieldName, goType, wrapper)
		writeScanField(&scanBody, structName, goName, fieldName, goType, required)
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
				emitScalarField(siblingGoName, sibling.Name, "string", "Field", sibling.Def.IsRequired)
				siblingEmitted[siblingName] = true
			}
			selfGoName := pascalCase(f.Name)
			if err := claimFieldName(selfGoName, fmt.Sprintf("field %q", f.Name)); err != nil {
				return nil, err
			}
			emitScalarField(selfGoName, f.Name, "string", "Field", f.Def.IsRequired)

		case model.KindMany2One:
			if !strings.HasSuffix(f.Name, "_id") {
				return nil, fmt.Errorf("field %q: a Many2One field's name must end in \"_id\" (go-sdk-reference.md §22 \"Many2One\")", f.Name)
			}
			fkGoName := pascalCase(f.Name)
			if err := claimFieldName(fkGoName, fmt.Sprintf("field %q", f.Name)); err != nil {
				return nil, err
			}
			emitScalarField(fkGoName, f.Name, "string", "Field", f.Def.IsRequired || f.Def.IsPrimaryKey)

			expansionName := strings.TrimSuffix(f.Name, "_id")
			expansionGoName := pascalCase(expansionName)
			if err := claimFieldName(expansionGoName, fmt.Sprintf("field %q's Many2One expansion %q", f.Name, expansionName)); err != nil {
				return nil, err
			}
			fmt.Fprintf(&body, "\t%s *orm.RelationRef `db:%q`\n", expansionGoName, expansionName)
			// No field descriptor/AllFields entry: a *orm.RelationRef
			// expansion isn't a Condition-bearing column, just a
			// read-time convenience (issue #977's own scope note — see
			// #979/G for its eventual Ref[...] descriptor). Scan still
			// needs to decode it, via orm.RelationRef's own two-key
			// extraction from the nested {id, display_name} object
			// host_orm_relations.go's expandOneRelation attaches.
			writeScanRelationField(&scanBody, structName, expansionGoName, expansionName)

		case model.KindSelection:
			fieldGoName := pascalCase(f.Name)
			if err := claimFieldName(fieldGoName, fmt.Sprintf("field %q", f.Name)); err != nil {
				return nil, err
			}
			typeName, err := writeNamedTypeField(&body, &aux, structName, f.Name, fieldGoName, f.Def.SelectionValues, f.Def.IsRequired || f.Def.IsPrimaryKey)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", f.Name, err)
			}
			writeFieldDescriptor(&fieldsDecl, &fieldsInit, &allFields, structName, fieldGoName, f.Name, typeName, "Field")
			writeScanNamedTypeField(&scanBody, structName, fieldGoName, f.Name, typeName, f.Def.IsRequired || f.Def.IsPrimaryKey)

		case model.KindEnum:
			values, err := enumValues(types, f.Def.EnumType)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", f.Name, err)
			}
			fieldGoName := pascalCase(f.Name)
			if err := claimFieldName(fieldGoName, fmt.Sprintf("field %q", f.Name)); err != nil {
				return nil, err
			}
			typeName, err := writeNamedTypeField(&body, &aux, structName, f.Name, fieldGoName, values, f.Def.IsRequired || f.Def.IsPrimaryKey)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", f.Name, err)
			}
			writeFieldDescriptor(&fieldsDecl, &fieldsInit, &allFields, structName, fieldGoName, f.Name, typeName, "Field")
			writeScanNamedTypeField(&scanBody, structName, fieldGoName, f.Name, typeName, f.Def.IsRequired || f.Def.IsPrimaryKey)

		default:
			goType, err := baseGoType(f.Def.Kind)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", f.Name, err)
			}
			wrapper, err := fieldDescriptorWrapper(f.Def.Kind)
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
			emitScalarField(fieldGoName, f.Name, goType, wrapper, f.Def.IsRequired || f.Def.IsPrimaryKey)
		}
	}

	var buf bytes.Buffer
	buf.WriteString(generatedFileHeader)
	buf.WriteString("package models\n\n")
	buf.WriteString("import (\n")
	if usesTime {
		buf.WriteString("\t\"time\"\n\n")
	}
	// Always needed: ResourceName/Fields/AllFields/Scan below reference
	// orm.AnyField, the descriptor constructors, and orm.RelationRef,
	// regardless of which field kinds this particular model declares.
	buf.WriteString("\t\"github.com/djangbahevans/goerp/sdk/go/orm\"\n")
	buf.WriteString(")\n\n")

	fmt.Fprintf(&buf, "// %s corresponds to model %q.\n", structName, m.Name)
	fmt.Fprintf(&buf, "type %s struct {\n", structName)
	buf.Write(body.Bytes())
	buf.WriteString("}\n\n")

	fmt.Fprintf(&buf, "func (%s) ResourceName() string { return %q }\n\n", structName, m.Name)

	fmt.Fprintf(&buf, "// %sFields is one orm field descriptor per Condition-bearing column —\n", structName)
	fmt.Fprintf(&buf, "// a Many2One's *orm.RelationRef expansion has none (see the struct above).\n")
	fmt.Fprintf(&buf, "var %sFields = struct {\n", structName)
	buf.Write(fieldsDecl.Bytes())
	buf.WriteString("}{\n")
	buf.Write(fieldsInit.Bytes())
	buf.WriteString("}\n\n")

	fmt.Fprintf(&buf, "// %sAllFields is Query[%s]'s default Select() list — every\n", structName, structName)
	fmt.Fprintf(&buf, "// Condition-bearing field, in declaration order.\n")
	fmt.Fprintf(&buf, "var %sAllFields = []orm.AnyField[%s]{\n", structName, structName)
	buf.Write(allFields.Bytes())
	buf.WriteString("}\n\n")

	fmt.Fprintf(&buf, "// Scan populates x from row, a single record as host.orm returns it\n")
	fmt.Fprintf(&buf, "// (map[string]any, msgpack-decoded). Returns *orm.DecodeError naming\n")
	fmt.Fprintf(&buf, "// the field and value's actual type on a mismatch.\n")
	fmt.Fprintf(&buf, "func (x *%s) Scan(row map[string]any) error {\n", structName)
	buf.Write(scanBody.Bytes())
	buf.WriteString("\treturn nil\n")
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
// snake_case fallback.
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

// fieldDescriptorWrapper is goerp#977's field-kind -> orm field-
// descriptor table (go-sdk-reference.md §22/§26): which of
// Field/StringField/OrderedField/TimeField/BytesField (sdk/go/orm's
// field.go, goerp#973) a base-kind field's descriptor uses. Every kind
// here also has a baseGoType entry — this only adds which wrapper picks
// out the right extra operators (Like/ILike, Gt/Lt/Between, etc.) for
// that Go type; Selection/Enum/Many2One's FK column/DynamicLink always
// use plain Field (over the named type, or string) and don't call this.
func fieldDescriptorWrapper(k model.FieldKind) (string, error) {
	switch k {
	case model.KindChar, model.KindText:
		return "StringField", nil
	case model.KindUUID, model.KindSequence, model.KindTime:
		return "Field", nil
	case model.KindInteger, model.KindBigInt, model.KindFloat, model.KindDecimal:
		return "OrderedField", nil
	case model.KindBoolean:
		return "Field", nil
	case model.KindTimestampTZ, model.KindDate:
		return "TimeField", nil
	case model.KindJSONB, model.KindBytea:
		return "BytesField", nil
	default:
		return "", fmt.Errorf("unsupported field kind %s", k)
	}
}

// writeFieldDescriptor appends goName's field-descriptor member to
// <Struct>Fields' own declaration/init buffers, and its
// "<Struct>Fields.<goName>," entry to allFields. wrapper is one of
// Field/StringField/OrderedField/TimeField/BytesField; goType is only
// meaningful for Field and OrderedField, whose orm type takes a second
// type argument — StringField/TimeField/BytesField are always over
// string/time.Time/[]byte respectively, so their own New* constructor
// doesn't take one.
func writeFieldDescriptor(decl, init, allFields *bytes.Buffer, structName, goName, fieldName, goType, wrapper string) {
	switch wrapper {
	case "StringField":
		fmt.Fprintf(decl, "\t%s orm.StringField[%s]\n", goName, structName)
		fmt.Fprintf(init, "\t%s: orm.NewStringField[%s](%q),\n", goName, structName, fieldName)
	case "OrderedField":
		fmt.Fprintf(decl, "\t%s orm.OrderedField[%s, %s]\n", goName, structName, goType)
		fmt.Fprintf(init, "\t%s: orm.NewOrderedField[%s, %s](%q),\n", goName, structName, goType, fieldName)
	case "TimeField":
		fmt.Fprintf(decl, "\t%s orm.TimeField[%s]\n", goName, structName)
		fmt.Fprintf(init, "\t%s: orm.NewTimeField[%s](%q),\n", goName, structName, fieldName)
	case "BytesField":
		fmt.Fprintf(decl, "\t%s orm.BytesField[%s]\n", goName, structName)
		fmt.Fprintf(init, "\t%s: orm.NewBytesField[%s](%q),\n", goName, structName, fieldName)
	default: // "Field"
		fmt.Fprintf(decl, "\t%s orm.Field[%s, %s]\n", goName, structName, goType)
		fmt.Fprintf(init, "\t%s: orm.NewField[%s, %s](%q),\n", goName, structName, goType, fieldName)
	}
	fmt.Fprintf(allFields, "\t%sFields.%s,\n", structName, goName)
}

// writeScanField appends goName's decode block to Scan's own body — one
// type assertion against row[fieldName]'s raw msgpack-decoded value
// (goerp#960's own empirically-pinned per-FieldKind Go types), erroring
// via *orm.DecodeError on a mismatch. required controls both the
// assertion's own target type (never a pointer — host.orm's raw values
// are never themselves pointers) and whether a nil/absent value is
// tolerated: a required field simply left unscanned on a NULL/absent row
// value would silently zero-value it, the same footgun goerp#974
// replaced reflect.go specifically to catch, so only an optional field
// gets the "ok && v != nil" guard that skips scanning instead of
// asserting.
func writeScanField(buf *bytes.Buffer, structName, goName, fieldName, goType string, required bool) {
	if required {
		fmt.Fprintf(buf, "\tif v, ok := row[%q]; ok {\n", fieldName)
		fmt.Fprintf(buf, "\t\tval, ok := v.(%s)\n", goType)
		fmt.Fprintf(buf, "\t\tif !ok {\n")
		fmt.Fprintf(buf, "\t\t\treturn orm.NewDecodeError(%q, %q, %q, v)\n", structName, goName, goType)
		fmt.Fprintf(buf, "\t\t}\n")
		fmt.Fprintf(buf, "\t\tx.%s = val\n", goName)
		fmt.Fprintf(buf, "\t}\n")
		return
	}
	fmt.Fprintf(buf, "\tif v, ok := row[%q]; ok && v != nil {\n", fieldName)
	fmt.Fprintf(buf, "\t\tval, ok := v.(%s)\n", goType)
	fmt.Fprintf(buf, "\t\tif !ok {\n")
	fmt.Fprintf(buf, "\t\t\treturn orm.NewDecodeError(%q, %q, %q, v)\n", structName, goName, "*"+goType)
	fmt.Fprintf(buf, "\t\t}\n")
	fmt.Fprintf(buf, "\t\tx.%s = &val\n", goName)
	fmt.Fprintf(buf, "\t}\n")
}

// writeScanNamedTypeField is writeScanField's Selection/Enum
// counterpart: the raw value always decodes as a plain string
// (goerp#960's own empirical finding), converted to typeName after the
// assertion succeeds.
func writeScanNamedTypeField(buf *bytes.Buffer, structName, goName, fieldName, typeName string, required bool) {
	if required {
		fmt.Fprintf(buf, "\tif v, ok := row[%q]; ok {\n", fieldName)
		fmt.Fprintf(buf, "\t\ts, ok := v.(string)\n")
		fmt.Fprintf(buf, "\t\tif !ok {\n")
		fmt.Fprintf(buf, "\t\t\treturn orm.NewDecodeError(%q, %q, %q, v)\n", structName, goName, typeName)
		fmt.Fprintf(buf, "\t\t}\n")
		fmt.Fprintf(buf, "\t\tx.%s = %s(s)\n", goName, typeName)
		fmt.Fprintf(buf, "\t}\n")
		return
	}
	fmt.Fprintf(buf, "\tif v, ok := row[%q]; ok && v != nil {\n", fieldName)
	fmt.Fprintf(buf, "\t\ts, ok := v.(string)\n")
	fmt.Fprintf(buf, "\t\tif !ok {\n")
	fmt.Fprintf(buf, "\t\t\treturn orm.NewDecodeError(%q, %q, %q, v)\n", structName, goName, "*"+typeName)
	fmt.Fprintf(buf, "\t\t}\n")
	fmt.Fprintf(buf, "\t\tval := %s(s)\n", typeName)
	fmt.Fprintf(buf, "\t\tx.%s = &val\n", goName)
	fmt.Fprintf(buf, "\t}\n")
}

// writeScanRelationField appends a Many2One expansion field's decode
// block: the nested {id, display_name} object host_orm_relations.go's
// expandOneRelation attaches under readKey, extracted into an
// *orm.RelationRef — always optional, regardless of the FK column's own
// required-ness, since the expansion is nil whenever the related row is
// missing, RLS-excluded, or its module isn't loaded (fail-closed, not an
// error — host_orm_relations.go's own doc comment).
func writeScanRelationField(buf *bytes.Buffer, structName, goName, readKey string) {
	fmt.Fprintf(buf, "\tif v, ok := row[%q]; ok && v != nil {\n", readKey)
	fmt.Fprintf(buf, "\t\tnested, ok := v.(map[string]any)\n")
	fmt.Fprintf(buf, "\t\tif !ok {\n")
	fmt.Fprintf(buf, "\t\t\treturn orm.NewDecodeError(%q, %q, %q, v)\n", structName, goName, "*orm.RelationRef")
	fmt.Fprintf(buf, "\t\t}\n")
	fmt.Fprintf(buf, "\t\tref := orm.RelationRef{}\n")
	fmt.Fprintf(buf, "\t\tif id, ok := nested[\"id\"].(string); ok {\n")
	fmt.Fprintf(buf, "\t\t\tref.ID = id\n")
	fmt.Fprintf(buf, "\t\t}\n")
	fmt.Fprintf(buf, "\t\tif dn, ok := nested[\"display_name\"].(string); ok {\n")
	fmt.Fprintf(buf, "\t\t\tref.DisplayName = dn\n")
	fmt.Fprintf(buf, "\t\t}\n")
	fmt.Fprintf(buf, "\t\tx.%s = &ref\n", goName)
	fmt.Fprintf(buf, "\t}\n")
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
// typed as that named type. Returns the named type's own Go name, so the
// caller can build a field descriptor and Scan block for it.
func writeNamedTypeField(body, aux *bytes.Buffer, structName, fieldName, fieldGoName string, values []string, required bool) (string, error) {
	if fieldGoName == "" {
		return "", fmt.Errorf("no usable Go identifier for field name %q", fieldName)
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
	return typeName, nil
}
