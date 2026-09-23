package module

import (
	"bytes"
	"fmt"
	"go/format"
	"regexp"
	"strings"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// commonInitialisms is golint's own commonInitialisms list — id -> ID,
// url -> URL, not Id/Url (go-sdk-reference.md §22/goerp#961).
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
// on — not just "_", since a Selection/Enum value can carry any
// non-identifier separator ("in-progress", "needs review").
var identifierSplit = regexp.MustCompile(`[^A-Za-z0-9]+`)

// pascalCase converts a snake_case (or otherwise separator-delimited)
// identifier into the PascalCase Go identifier go-sdk-reference.md
// §22/goerp#961 generates field and type names as.
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
// (go-sdk-reference.md §22/§26, goerp#973/#974). types is schema.Schema's
// own Types slice, needed to resolve an Enum field's declared values.
func renderModelFile(m *model.ModelDeclaration, types []model.TypeDeclaration) ([]byte, error) {
	structName := pascalCase(m.ResourceName())
	if structName == "" {
		return nil, fmt.Errorf("model %q has no usable resource name to generate a struct from", m.Name)
	}

	// A DynamicLink field's sibling Selection field is emitted alongside
	// it as a plain string (go-sdk-reference.md §22 "DynamicLink"), not
	// on its own when the loop reaches it.
	fieldByName := make(map[string]model.NamedField, len(m.Fields))
	siblingOfDynamicLink := map[string]bool{}
	for _, f := range m.Fields {
		fieldByName[f.Name] = f
		if f.Def.Kind == model.KindDynamicLink {
			siblingOfDynamicLink[f.Def.ReferenceTypeField] = true
		}
	}

	var body, aux, fieldsDecl, fieldsInit, allFields, scanBody, valuesSetters bytes.Buffer
	usesTime := false
	siblingEmitted := map[string]bool{}

	// usedFieldNames catches two fields whose generated Go names
	// collide — go/format.Source only parses and formats, it doesn't
	// type-check (goerp#970).
	usedFieldNames := map[string]string{}
	claimFieldName := func(goName, source string) error {
		if prior, ok := usedFieldNames[goName]; ok {
			return fmt.Errorf("%s and %s both generate the Go field name %q — rename one", prior, source, goName)
		}
		usedFieldNames[goName] = source
		return nil
	}

	// emitScalarField renders one Condition-bearing field's struct line,
	// descriptor, AllFields entry, and Scan block together, plus a
	// <Struct>Values.SetX builder method when writable is set.
	emitScalarField := func(goName, fieldName, goType, wireType, wrapper string, required, writable bool) {
		writeField(&body, goName, fieldName, goType, required)
		writeFieldDescriptor(&fieldsDecl, &fieldsInit, &allFields, structName, goName, fieldName, goType, wrapper)
		writeScanField(&scanBody, structName, goName, fieldName, goType, wireType, required)
		if writable {
			writeValueSetter(&valuesSetters, structName, goName, goType, wrapper)
		}
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
				emitScalarField(siblingGoName, sibling.Name, "string", "string", "Field", sibling.Def.IsRequired || sibling.Def.IsPrimaryKey, isWritable(sibling.Def))
				siblingEmitted[siblingName] = true
			}
			selfGoName := pascalCase(f.Name)
			if err := claimFieldName(selfGoName, fmt.Sprintf("field %q", f.Name)); err != nil {
				return nil, err
			}
			emitScalarField(selfGoName, f.Name, "string", "string", "Field", f.Def.IsRequired || f.Def.IsPrimaryKey, isWritable(f.Def))

		case model.KindMany2One:
			if !strings.HasSuffix(f.Name, "_id") {
				return nil, fmt.Errorf("field %q: a Many2One field's name must end in \"_id\" (go-sdk-reference.md §22 \"Many2One\")", f.Name)
			}
			fkGoName := pascalCase(f.Name)
			if err := claimFieldName(fkGoName, fmt.Sprintf("field %q", f.Name)); err != nil {
				return nil, err
			}
			emitScalarField(fkGoName, f.Name, "string", "string", "Field", f.Def.IsRequired || f.Def.IsPrimaryKey, isWritable(f.Def))

			expansionName := strings.TrimSuffix(f.Name, "_id")
			expansionGoName := pascalCase(expansionName)
			if err := claimFieldName(expansionGoName, fmt.Sprintf("field %q's Many2One expansion %q", f.Name, expansionName)); err != nil {
				return nil, err
			}
			fmt.Fprintf(&body, "\t%s *orm.RelationRef `db:%q`\n", expansionGoName, expansionName)
			// No field descriptor/AllFields entry — not Condition-bearing
			// (see #979/G for its eventual Ref[...] descriptor).
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
			if isWritable(f.Def) {
				writeValueSetter(&valuesSetters, structName, fieldGoName, typeName, "Field")
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
			typeName, err := writeNamedTypeField(&body, &aux, structName, f.Name, fieldGoName, values, f.Def.IsRequired || f.Def.IsPrimaryKey)
			if err != nil {
				return nil, fmt.Errorf("field %q: %w", f.Name, err)
			}
			writeFieldDescriptor(&fieldsDecl, &fieldsInit, &allFields, structName, fieldGoName, f.Name, typeName, "Field")
			writeScanNamedTypeField(&scanBody, structName, fieldGoName, f.Name, typeName, f.Def.IsRequired || f.Def.IsPrimaryKey)
			if isWritable(f.Def) {
				writeValueSetter(&valuesSetters, structName, fieldGoName, typeName, "Field")
			}

		default:
			goType, wireType, wrapper, err := fieldGoType(f.Def.Kind)
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
			emitScalarField(fieldGoName, f.Name, goType, wireType, wrapper, f.Def.IsRequired || f.Def.IsPrimaryKey, isWritable(f.Def))
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
	// orm regardless of which field kinds this model declares.
	buf.WriteString("\t\"github.com/djangbahevans/goerp/sdk/go/orm\"\n")
	buf.WriteString(")\n\n")

	fmt.Fprintf(&buf, "// %s corresponds to model %q.\n", structName, m.Name)
	fmt.Fprintf(&buf, "type %s struct {\n", structName)
	buf.Write(body.Bytes())
	buf.WriteString("}\n\n")

	fmt.Fprintf(&buf, "func (%s) ResourceName() string { return %q }\n\n", structName, m.Name)

	fmt.Fprintf(&buf, "// %sFields is one orm field descriptor per Condition-bearing column.\n", structName)
	fmt.Fprintf(&buf, "var %sFields = struct {\n", structName)
	buf.Write(fieldsDecl.Bytes())
	buf.WriteString("}{\n")
	buf.Write(fieldsInit.Bytes())
	buf.WriteString("}\n\n")

	fmt.Fprintf(&buf, "// %sAllFields is Query[%s]'s default Select() list.\n", structName, structName)
	fmt.Fprintf(&buf, "var %sAllFields = []orm.AnyField[%s]{\n", structName, structName)
	buf.Write(allFields.Bytes())
	buf.WriteString("}\n\n")

	fmt.Fprintf(&buf, "// Scan populates x from row, a single record as host.orm returns it.\n")
	fmt.Fprintf(&buf, "func (x *%s) Scan(row map[string]any) error {\n", structName)
	buf.Write(scanBody.Bytes())
	buf.WriteString("\treturn nil\n")
	buf.WriteString("}\n\n")

	fmt.Fprintf(&buf, "// %sValues is a typed builder for %s's writable fields — one SetX\n", structName, structName)
	fmt.Fprintf(&buf, "// method per field goerp module generate found writable (not Readonly,\n")
	fmt.Fprintf(&buf, "// not Computed). The same builder serves both Create and Write: Create\n")
	fmt.Fprintf(&buf, "// sends every key present, Write sends only the keys present.\n")
	fmt.Fprintf(&buf, "type %sValues struct {\n\torm.Values[%s]\n}\n\n", structName, structName)
	fmt.Fprintf(&buf, "func New%sValues() *%sValues {\n\treturn &%sValues{Values: *orm.NewValues[%s]()}\n}\n\n", structName, structName, structName, structName)
	buf.Write(valuesSetters.Bytes())

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

// writeField renders one struct field line: goType is pointer-prefixed
// unless required, and the db tag uses fieldName verbatim.
func writeField(buf *bytes.Buffer, goName, fieldName, goType string, required bool) {
	if !required {
		goType = "*" + goType
	}
	fmt.Fprintf(buf, "\t%s %s `db:%q`\n", goName, goType, fieldName)
}

// fieldGoType is goerp#960/#977's field-kind table: the struct field's
// own Go type, the type Scan asserts the raw msgpack-decoded value
// against, and which orm field-descriptor wrapper it uses. wireType
// differs from goType only for KindInteger — msgpack's int wire format
// doesn't preserve the encoder's original int32 width, so a Postgres
// int4 column decodes as int64 on the guest, empirically confirmed via a
// real round trip. Many2One, Selection, Enum, DynamicLink, and One2Many
// each have their own generation shape and never reach this function.
func fieldGoType(k model.FieldKind) (goType, wireType, wrapper string, err error) {
	switch k {
	case model.KindInteger:
		return "int32", "int64", "OrderedField", nil
	case model.KindBigInt:
		return "int64", "int64", "OrderedField", nil
	case model.KindFloat:
		return "float64", "float64", "OrderedField", nil
	case model.KindDecimal:
		return "string", "string", "OrderedField", nil
	case model.KindBoolean:
		return "bool", "bool", "Field", nil
	case model.KindChar, model.KindText:
		return "string", "string", "StringField", nil
	case model.KindUUID, model.KindSequence, model.KindTime:
		return "string", "string", "Field", nil
	case model.KindTimestampTZ, model.KindDate:
		return "time.Time", "time.Time", "TimeField", nil
	case model.KindJSONB, model.KindBytea:
		return "[]byte", "[]byte", "BytesField", nil
	default:
		return "", "", "", fmt.Errorf("unsupported field kind %s", k)
	}
}

// writeFieldDescriptor appends goName's field-descriptor member to
// <Struct>Fields' declaration/init buffers and its AllFields entry.
// goType is only meaningful for Field/OrderedField, whose orm type takes
// a second type argument.
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

// isWritable reports whether def's field gets a <Struct>Values.SetX
// method — the compile-time counterpart to host.orm's own runtime
// orm.field_not_writable rejection (internal/engine/wasm/host_orm_write.go's
// buildAssignment), which also rejects a Computed or Readonly field.
// buildAssignment's third case, a KindOne2Many field, never reaches
// isWritable at all — the caller's own loop skips One2Many entirely
// before generating anything for it (no backing column to write).
func isWritable(def model.FieldDef) bool {
	return !def.IsReadonly && !def.IsComputed
}

// writeValueSetter appends goName's builder method to <Struct>Values.
// Every wrapper goes through orm.Set except BytesField, which needs
// orm.SetBytes (BytesField has no Field[T,TValue] to satisfy Set's
// parameter type). StringField/OrderedField/TimeField's own descriptor
// type doesn't itself satisfy Set's Field[T,TValue] parameter — only the
// Field it embeds does — so the call addresses that embedded field via
// ".Field".
func writeValueSetter(buf *bytes.Buffer, structName, goName, goType, wrapper string) {
	fmt.Fprintf(buf, "func (v *%sValues) Set%s(x %s) *%sValues {\n", structName, goName, goType, structName)
	switch wrapper {
	case "BytesField":
		fmt.Fprintf(buf, "\torm.SetBytes(&v.Values, %sFields.%s, x)\n", structName, goName)
	case "StringField", "OrderedField", "TimeField":
		fmt.Fprintf(buf, "\torm.Set(&v.Values, %sFields.%s.Field, x)\n", structName, goName)
	default: // "Field"
		fmt.Fprintf(buf, "\torm.Set(&v.Values, %sFields.%s, x)\n", structName, goName)
	}
	fmt.Fprintf(buf, "\treturn v\n")
	fmt.Fprintf(buf, "}\n\n")
}

// writeScanField appends goName's decode block to Scan's own body: one
// type assertion against row[fieldName]'s raw value (asserted as
// wireType, then narrowed to goType if they differ), erroring via
// *orm.DecodeError on a mismatch. A required field errors on a NULL/
// absent value rather than silently zero-valuing it; an optional field
// is simply left unscanned.
func writeScanField(buf *bytes.Buffer, structName, goName, fieldName, goType, wireType string, required bool) {
	assign := "val"
	if wireType != goType {
		assign = fmt.Sprintf("%s(val)", goType)
	}
	if required {
		fmt.Fprintf(buf, "\tif v, ok := row[%q]; ok {\n", fieldName)
		fmt.Fprintf(buf, "\t\tval, ok := v.(%s)\n", wireType)
		fmt.Fprintf(buf, "\t\tif !ok {\n")
		fmt.Fprintf(buf, "\t\t\treturn orm.NewDecodeError(%q, %q, %q, v)\n", structName, goName, goType)
		fmt.Fprintf(buf, "\t\t}\n")
		fmt.Fprintf(buf, "\t\tx.%s = %s\n", goName, assign)
		fmt.Fprintf(buf, "\t}\n")
		return
	}
	fmt.Fprintf(buf, "\tif v, ok := row[%q]; ok && v != nil {\n", fieldName)
	fmt.Fprintf(buf, "\t\tval, ok := v.(%s)\n", wireType)
	fmt.Fprintf(buf, "\t\tif !ok {\n")
	fmt.Fprintf(buf, "\t\t\treturn orm.NewDecodeError(%q, %q, %q, v)\n", structName, goName, "*"+goType)
	fmt.Fprintf(buf, "\t\t}\n")
	if wireType != goType {
		fmt.Fprintf(buf, "\t\tconverted := %s\n", assign)
		fmt.Fprintf(buf, "\t\tx.%s = &converted\n", goName)
	} else {
		fmt.Fprintf(buf, "\t\tx.%s = &val\n", goName)
	}
	fmt.Fprintf(buf, "\t}\n")
}

// writeScanNamedTypeField is writeScanField's Selection/Enum
// counterpart: the raw value always decodes as a plain string,
// converted to typeName after the assertion succeeds.
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
// block: the nested {id, display_name} object host_orm_relations.go
// attaches under readKey, extracted into an *orm.RelationRef — always
// optional, since the expansion is nil whenever there's nothing to
// resolve (fail-closed, not an error).
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
// own Types slice (go-sdk-reference.md §22 "Enum types").
func enumValues(types []model.TypeDeclaration, enumType string) ([]string, error) {
	for _, t := range types {
		if t.Name == enumType {
			return t.Values, nil
		}
	}
	return nil, fmt.Errorf("enum type %q not declared in schema.Types", enumType)
}

// writeNamedTypeField is Selection and KindEnum's shared generation
// shape: a named string type plus one constant per value
// (go-sdk-reference.md §22/goerp#961), plus the struct field itself.
// Returns the named type's own Go name, for the caller's descriptor and
// Scan block.
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
