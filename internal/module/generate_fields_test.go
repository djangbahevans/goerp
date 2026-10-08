package module

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

var collapseSpace = regexp.MustCompile(`[ \t]+`)

func testGenContext() genContext {
	return genContext{moduleName: "widgets", dependsOn: []string{"contacts", "hr"}}
}

// normalizeSpaces collapses gofmt's own column-alignment padding (runs
// of spaces between a struct field's name/type/tag) down to one space,
// so an assertion on a generated line's content doesn't have to predict
// gofmt's alignment width across a whole struct.
func normalizeSpaces(s string) string {
	return collapseSpace.ReplaceAllString(s, " ")
}

func TestPascalCase(t *testing.T) {
	tests := []struct{ in, want string }{
		{"id", "ID"},
		{"customer_id", "CustomerID"},
		{"created_at", "CreatedAt"},
		{"url", "URL"},
		{"needs_review", "NeedsReview"},
		{"draft", "Draft"},
		{"name", "Name"},
	}
	for _, tt := range tests {
		if got := pascalCase(tt.in); got != tt.want {
			t.Errorf("pascalCase(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestModelPackageIdentifiers_FixedSet(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("name", model.Text().Required())

	ids, err := modelPackageIdentifiers(m, nil)
	if err != nil {
		t.Fatalf("modelPackageIdentifiers: %v", err)
	}

	want := []string{"Gadget", "GadgetFields", "GadgetAllFields", "GadgetValues", "NewGadgetValues"}
	for _, id := range want {
		if !slices.Contains(ids, id) {
			t.Errorf("modelPackageIdentifiers = %v, missing %q", ids, id)
		}
	}
}

func TestModelPackageIdentifiers_SelectionAndEnum(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("state", model.Selection("draft", "done").Required()).
		Field("priority", model.Enum("gadget_priority_enum").Required())

	types := []model.TypeDeclaration{
		model.EnumType("gadget_priority_enum", "low", "high"),
	}

	ids, err := modelPackageIdentifiers(m, types)
	if err != nil {
		t.Fatalf("modelPackageIdentifiers: %v", err)
	}

	want := []string{
		"GadgetState", "GadgetStateDraft", "GadgetStateDone",
		"GadgetPriority", "GadgetPriorityLow", "GadgetPriorityHigh",
	}
	for _, id := range want {
		if !slices.Contains(ids, id) {
			t.Errorf("modelPackageIdentifiers = %v, missing %q", ids, id)
		}
	}
}

func TestModelPackageIdentifiers_UnknownEnumType_Errors(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("priority", model.Enum("does_not_exist").Required())

	if _, err := modelPackageIdentifiers(m, nil); err == nil {
		t.Fatal("expected an error for an Enum field naming an undeclared type")
	}
}

func TestModelPackageIdentifiers_DynamicLinkSiblingNotClaimed(t *testing.T) {
	m := model.Define("widgets.attachment").
		Field("reference_type", model.Selection("widgets.widget", "widgets.gadget").Required()).
		Field("reference_id", model.DynamicLink("reference_type").Required())

	ids, err := modelPackageIdentifiers(m, nil)
	if err != nil {
		t.Fatalf("modelPackageIdentifiers: %v", err)
	}

	for _, phantom := range []string{"AttachmentReferenceType", "AttachmentReferenceTypeWidgetsWidget", "AttachmentReferenceTypeWidgetsGadget"} {
		if slices.Contains(ids, phantom) {
			t.Errorf("modelPackageIdentifiers = %v, wrongly claims %q — a DynamicLink's sibling Selection field is a plain string, not a named type", ids, phantom)
		}
	}
}

// Identifier prediction must match rendered output because collision checks run before
// rendering.
func TestModelPackageIdentifiers_MatchesRenderModelFileOutput(t *testing.T) {
	m := model.Define("widgets.gadget", model.Table("gadgets")).
		WithStandardFields().
		Field("name", model.Text().Required()).
		Field("state", model.Selection("draft", "done").Required()).
		Field("priority", model.Enum("gadget_priority_enum").Required()).
		// A DynamicLink discriminator is a plain string and must not claim Selection type
		// or constant identifiers.
		Field("reference_type", model.Selection("widgets.widget", "widgets.gadget").Required()).
		Field("reference_id", model.DynamicLink("reference_type").Required())

	types := []model.TypeDeclaration{
		model.EnumType("gadget_priority_enum", "low", "high"),
	}

	ids, err := modelPackageIdentifiers(m, types)
	if err != nil {
		t.Fatalf("modelPackageIdentifiers: %v", err)
	}

	out, _, err := renderModelFile(m, types, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := string(out)

	for _, id := range ids {
		if !strings.Contains(src, id) {
			t.Errorf("modelPackageIdentifiers claims %q, but it doesn't appear in renderModelFile's own output:\n%s", id, src)
		}
	}

	// Every identifier emitted by rendering must also be claimed for collision checks.
	for _, decl := range topLevelDeclarations(t, src) {
		if !slices.Contains(ids, decl) {
			t.Errorf("renderModelFile declares %q, but modelPackageIdentifiers doesn't claim it", decl)
		}
	}
}

// topLevelDeclarations extracts every package-level identifier a
// generated model file declares — a type, a var, a package-level func
// (no receiver), or a const inside a "const (" block — for
// TestModelPackageIdentifiers_MatchesRenderModelFileOutput's own reverse-
// direction check.
var (
	topLevelTypeOrVarRe = regexp.MustCompile(`^(?:type|var)\s+(\w+)\b`)
	topLevelFuncRe      = regexp.MustCompile(`^func\s+(\w+)\(`)
	constNameRe         = regexp.MustCompile(`^\t(\w+)\s`)
)

func topLevelDeclarations(t *testing.T, src string) []string {
	t.Helper()
	var decls []string
	inConstBlock := false
	for line := range strings.Lines(src) {
		switch {
		case strings.HasPrefix(line, "const ("):
			inConstBlock = true
		case inConstBlock && strings.HasPrefix(line, ")"):
			inConstBlock = false
		case inConstBlock:
			if m := constNameRe.FindStringSubmatch(line); m != nil {
				decls = append(decls, m[1])
			}
		default:
			if m := topLevelTypeOrVarRe.FindStringSubmatch(line); m != nil {
				decls = append(decls, m[1])
			} else if m := topLevelFuncRe.FindStringSubmatch(line); m != nil {
				decls = append(decls, m[1])
			}
		}
	}
	return decls
}

func TestRenderModelFile_BasicFieldKinds(t *testing.T) {
	m := model.Define("widgets.widget", model.Table("widgets")).
		WithStandardFields().
		Field("name", model.Text().Required()).
		Field("quantity", model.Integer().Required()).
		Field("big_quantity", model.BigInt().Required()).
		Field("weight", model.Float().Required()).
		Field("is_active", model.Boolean().Required())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.HasPrefix(src, "// Code generated by goerp module generate. DO NOT EDIT.\n") {
		t.Errorf("missing DO NOT EDIT header:\n%s", src)
	}
	if !strings.Contains(src, "type Widget struct {") {
		t.Errorf("struct name = want Widget:\n%s", src)
	}

	wantLines := []string{
		`ID string `,
		`TenantID string `,
		`CreatedAt time.Time `,
		`UpdatedAt time.Time `,
		`DeletedAt *time.Time `,
		`CreatedBy *string `,
		`Etag string `,
		`Name string `,
		`Quantity int32 `,
		`BigQuantity int64 `,
		`Weight float64 `,
		`IsActive bool `,
	}
	for _, want := range wantLines {
		if !strings.Contains(src, want) {
			t.Errorf("output missing field %q:\n%s", want, src)
		}
	}
	if !strings.Contains(src, `"time"`) {
		t.Errorf("output should import \"time\" for TimestampTZ fields:\n%s", src)
	}
}

func TestRenderModelFile_NullableVsRequired(t *testing.T) {
	m := model.Define("widgets.widget").
		Field("id", model.UUID().PrimaryKey()).
		Field("required_name", model.Text().Required()).
		Field("optional_name", model.Text())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, "ID string `db:\"id\"`") {
		t.Errorf("primary key should be non-pointer string:\n%s", src)
	}
	if !strings.Contains(src, "RequiredName string `db:\"required_name\"`") {
		t.Errorf("required field should be non-pointer:\n%s", src)
	}
	if !strings.Contains(src, "OptionalName *string `db:\"optional_name\"`") {
		t.Errorf("non-required field should be a pointer:\n%s", src)
	}
}

func TestRenderModelFile_SelectionGeneratesNamedTypeAndConstants(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("state", model.Selection("draft", "needs_review", "done").Required())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, "State GadgetState `db:\"state\"`") {
		t.Errorf("struct field should use the named GadgetState type:\n%s", src)
	}
	if !strings.Contains(src, "type GadgetState string") {
		t.Errorf("missing named type declaration:\n%s", src)
	}
	for _, want := range []string{
		`GadgetStateDraft GadgetState = "draft"`,
		`GadgetStateNeedsReview GadgetState = "needs_review"`,
		`GadgetStateDone GadgetState = "done"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("missing constant %q:\n%s", want, src)
		}
	}
}

func TestRenderModelFile_EnumResolvesValuesFromSchemaTypes(t *testing.T) {
	m := model.Define("widgets.kind_probe").
		Field("priority", model.Enum("kind_probe_priority_enum").Required())

	types := []model.TypeDeclaration{
		model.EnumType("kind_probe_priority_enum", "low", "high"),
	}

	out, _, err := renderModelFile(m, types, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, "Priority KindProbePriority `db:\"priority\"`") {
		t.Errorf("enum field should use the named KindProbePriority type:\n%s", src)
	}
	if !strings.Contains(src, `KindProbePriorityLow KindProbePriority = "low"`) ||
		!strings.Contains(src, `KindProbePriorityHigh KindProbePriority = "high"`) {
		t.Errorf("missing enum constants:\n%s", src)
	}
}

func TestRenderModelFile_UnknownEnumType_Errors(t *testing.T) {
	m := model.Define("widgets.kind_probe").
		Field("priority", model.Enum("does_not_exist").Required())

	if _, _, err := renderModelFile(m, nil, testGenContext()); err == nil {
		t.Fatal("expected an error for an Enum field naming an undeclared type")
	}
}

func TestRenderModelFile_Many2OneCrossModuleGeneratesFKAndMarkerRefExpansion(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("customer_id", model.Many2One("contacts.contact").Required())

	out, markers, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, `CustomerID string `+"`db:\"customer_id\"`") {
		t.Errorf("missing FK field:\n%s", src)
	}
	if !strings.Contains(src, `Customer orm.Ref[ContactsContactRef] `+"`db:\"customer\"`") {
		t.Errorf("missing orm.Ref[ContactsContactRef] expansion field:\n%s", src)
	}
	if !strings.Contains(src, `"github.com/djangbahevans/goerp/sdk/go/orm"`) {
		t.Errorf("output should import sdk/go/orm for Ref:\n%s", src)
	}

	if len(markers) != 1 || markers[0].goName != "ContactsContactRef" || markers[0].resourceName != "contacts.contact" {
		t.Errorf("markers = %+v, want one {ContactsContactRef, contacts.contact}", markers)
	}
}

// PascalCase concatenation can map different resource pairs to the same marker name, so
// deduplication must compare resource identity too.
func TestRenderCrossModuleRefsFile_DistinctResourcesCollidingOnGoName_Errors(t *testing.T) {
	markers := []crossModuleMarker{
		{goName: "ABCRef", resourceName: "a_b.c"},
		{goName: "ABCRef", resourceName: "a.b_c"},
	}

	if _, _, err := renderCrossModuleRefsFile(markers); err == nil {
		t.Fatal("expected an error for two distinct related_model values colliding on the same generated Go name")
	}
}

func TestRenderCrossModuleRefsFile_SameResourceReferencedTwice_Dedups(t *testing.T) {
	markers := []crossModuleMarker{
		{goName: "ContactsContactRef", resourceName: "contacts.contact"},
		{goName: "ContactsContactRef", resourceName: "contacts.contact"},
	}

	out, _, err := renderCrossModuleRefsFile(markers)
	if err != nil {
		t.Fatalf("renderCrossModuleRefsFile: %v", err)
	}
	if n := strings.Count(string(out), "type ContactsContactRef struct{}"); n != 1 {
		t.Errorf("ContactsContactRef declared %d times, want exactly 1:\n%s", n, out)
	}
}

func TestRenderModelFile_Many2OneSameModuleGeneratesRealTargetStruct(t *testing.T) {
	gadget := model.Define("widgets.gadget").
		Field("name", model.Text())
	probe := model.Define("widgets.kind_probe").
		Field("created_by_gadget_id", model.Many2One("widgets.gadget"))

	ctx := genContext{
		moduleName:       "widgets",
		modelsByResource: map[string]*model.ModelDeclaration{"gadget": gadget},
	}

	out, markers, err := renderModelFile(probe, nil, ctx)
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, `CreatedByGadget orm.Ref[Gadget] `+"`db:\"created_by_gadget\"`") {
		t.Errorf("missing orm.Ref[Gadget] expansion field:\n%s", src)
	}
	if len(markers) != 0 {
		t.Errorf("markers = %+v, want none for a same-module target", markers)
	}
}

func TestRenderModelFile_Many2OneCrossModuleTargetNotInDependsOn_Errors(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("owner_id", model.Many2One("nobody.person").Required())

	if _, _, err := renderModelFile(m, nil, testGenContext()); err == nil {
		t.Fatal("expected an error for a cross-module target whose module isn't in depends_on or soft_depends_on")
	}
}

func TestRenderModelFile_Many2OneSameModuleTargetNotDeclared_Errors(t *testing.T) {
	m := model.Define("widgets.kind_probe").
		Field("created_by_gadget_id", model.Many2One("widgets.gadget").Required())

	ctx := genContext{moduleName: "widgets", modelsByResource: map[string]*model.ModelDeclaration{}}

	if _, _, err := renderModelFile(m, nil, ctx); err == nil {
		t.Fatal("expected an error for a same-module target not declared in this module's own schema")
	}
}

func TestRenderModelFile_DynamicLinkGeneratesTwoPlainFields(t *testing.T) {
	m := model.Define("widgets.attachment").
		Field("reference_type", model.Selection("sales.order", "contacts.contact").Required()).
		Field("reference_id", model.DynamicLink("reference_type").Required())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, `ReferenceType string `+"`db:\"reference_type\"`") {
		t.Errorf("missing plain ReferenceType field:\n%s", src)
	}
	if !strings.Contains(src, `ReferenceID string `+"`db:\"reference_id\"`") {
		t.Errorf("missing plain ReferenceID field:\n%s", src)
	}
	// DynamicLink's sibling Selection field must not also generate its
	// own named-string-type version (go-sdk-reference.md §22
	// "DynamicLink" — it names an existing sibling, not a separate
	// allowlist).
	if strings.Contains(src, "AttachmentReferenceType") {
		t.Errorf("sibling Selection field should not generate a separate named type:\n%s", src)
	}
}

// Primary keys are required even when IsRequired is false, including DynamicLink fields.
func TestRenderModelFile_DynamicLinkPrimaryKeyIsNonPointer(t *testing.T) {
	m := model.Define("widgets.link").
		Field("reference_type", model.Selection("sales.order", "contacts.contact").PrimaryKey()).
		Field("reference_id", model.DynamicLink("reference_type").PrimaryKey())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, `ReferenceType string `+"`db:\"reference_type\"`") {
		t.Errorf("PK sibling field should be non-pointer:\n%s", src)
	}
	if !strings.Contains(src, `ReferenceID string `+"`db:\"reference_id\"`") {
		t.Errorf("PK DynamicLink field should be non-pointer:\n%s", src)
	}
}

// DynamicLink fields sharing a discriminator must emit its struct field once; formatting
// alone cannot detect duplicates.
func TestRenderModelFile_TwoDynamicLinkFieldsSharingOneSibling_NoDuplicateField(t *testing.T) {
	m := model.Define("widgets.link").
		Field("reference_type", model.Selection("widgets.widget", "widgets.gadget").Required()).
		Field("source_id", model.DynamicLink("reference_type").Required()).
		Field("target_id", model.DynamicLink("reference_type").Required())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if n := strings.Count(src, "ReferenceType string"); n != 1 {
		t.Errorf("ReferenceType field emitted %d times, want exactly 1:\n%s", n, src)
	}
	if !strings.Contains(src, `SourceID string `+"`db:\"source_id\"`") {
		t.Errorf("missing SourceID field:\n%s", src)
	}
	if !strings.Contains(src, `TargetID string `+"`db:\"target_id\"`") {
		t.Errorf("missing TargetID field:\n%s", src)
	}
}

// Many2One expansion strips _id; without that suffix, its Go name would duplicate the
// foreign-key field.
func TestRenderModelFile_Many2OneFieldNotEndingInID_Errors(t *testing.T) {
	m := model.Define("hr.employee").
		Field("manager", model.Many2One("hr.employee").Required())

	if _, _, err := renderModelFile(m, nil, testGenContext()); err == nil {
		t.Fatal("expected an error for a Many2One field not ending in \"_id\"")
	}
}

// Formatting accepts duplicate struct fields; generation must detect collisions before
// emitting non-compiling Go.
func TestRenderModelFile_Many2OneExpansionCollidesWithSiblingField_Errors(t *testing.T) {
	m := model.Define("hr.employee").
		Field("manager_id", model.Many2One("hr.employee")).
		Field("manager", model.Text())

	_, _, err := renderModelFile(m, nil, testGenContext())
	if err == nil {
		t.Fatal("expected an error for manager_id's Many2One expansion colliding with the sibling manager field")
	}
	if !strings.Contains(err.Error(), "manager_id") || !strings.Contains(err.Error(), `"manager"`) || !strings.Contains(err.Error(), "Manager") {
		t.Errorf("error = %q, want it to name both manager_id and manager and the colliding Go name Manager", err)
	}
}

func TestRenderModelFile_FieldNamesCollidingOnPascalCase_Errors(t *testing.T) {
	m := model.Define("widgets.widget").
		Field("display_name", model.Text()).
		Field("display-name", model.Text())

	if _, _, err := renderModelFile(m, nil, testGenContext()); err == nil {
		t.Fatal("expected an error for two field names that PascalCase to the same Go identifier")
	}
}

// Selection values can contain separators, so constant names must convert them into valid
// Go identifiers.
func TestRenderModelFile_SelectionValueWithNonIdentifierChars(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("state", model.Selection("in-progress", "needs review").Required())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, `GadgetStateInProgress GadgetState = "in-progress"`) {
		t.Errorf("missing GadgetStateInProgress constant:\n%s", src)
	}
	if !strings.Contains(src, `GadgetStateNeedsReview GadgetState = "needs review"`) {
		t.Errorf("missing GadgetStateNeedsReview constant:\n%s", src)
	}
}

// Different Selection spellings can generate one constant name; reject collisions before
// formatting accepts duplicate declarations.
func TestRenderModelFile_SelectionDuplicateConstantName_Errors(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("state", model.Selection("in-progress", "in_progress").Required())

	if _, _, err := renderModelFile(m, nil, testGenContext()); err == nil {
		t.Fatal("expected an error for two Selection values colliding on the same generated constant name")
	}
}

// Selection type names must not collide with the model's fixed descriptor identifiers.
func TestRenderModelFile_SelectionFieldNameCollidesWithFieldsDescriptor_Errors(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("fields", model.Selection("a", "b").Required())

	if _, _, err := renderModelFile(m, nil, testGenContext()); err == nil {
		t.Fatal("expected an error for a Selection field named \"fields\" colliding with the generated GadgetFields descriptor")
	}
}

func TestRenderModelFile_EmptyResourceName_Errors(t *testing.T) {
	m := model.Define("")

	if _, _, err := renderModelFile(m, nil, testGenContext()); err == nil {
		t.Fatal("expected an error for a model with no usable resource name")
	}
}

func TestRenderModelFile_One2ManyIsSkipped(t *testing.T) {
	m := model.Define("contacts.contact").
		Field("address_ids", model.One2Many("contacts.address", "contact_id"))

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if strings.Contains(src, "AddressIds") || strings.Contains(src, "AddressIDs") {
		t.Errorf("One2Many field should be skipped entirely:\n%s", src)
	}
}

func TestRenderModelFile_UnsupportedFieldKind_Errors(t *testing.T) {
	m := model.Define("widgets.widget").
		Field("counter", model.Sequence("{year}-{seq:04}").Required())

	// Sequence is supported (string) — this test instead pins that an
	// actually-unrecognized FieldKind value surfaces a clear error
	// rather than silently emitting a broken field, by forging one
	// beyond the declared enum's range.
	m.Fields[0].Def.Kind = model.FieldKind(9999)

	if _, _, err := renderModelFile(m, nil, testGenContext()); err == nil {
		t.Fatal("expected an error for an unsupported field kind")
	}
}

func TestRenderModelFile_GeneratesResourceNameMethod(t *testing.T) {
	m := model.Define("widgets.widget", model.Table("widgets")).
		WithStandardFields()

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, `func (Widget) ResourceName() string { return "widgets.widget" }`) {
		t.Errorf("missing ResourceName() method:\n%s", src)
	}
}

func TestRenderModelFile_FieldDescriptorsPickWrapperPerKind(t *testing.T) {
	m := model.Define("widgets.widget").
		Field("name", model.Text().Required()).
		Field("code", model.UUID().Required()).
		Field("quantity", model.Integer().Required()).
		Field("weight", model.Float().Required()).
		Field("is_active", model.Boolean().Required()).
		Field("opened_at", model.TimestampTZ().Required()).
		Field("attachment", model.Bytea()).
		Field("price", model.Decimal(10, 2).Required())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	for _, want := range []string{
		"Name orm.StringField[Widget]",
		"Code orm.Field[Widget, string]",
		"Quantity orm.OrderedField[Widget, int32]",
		"Weight orm.OrderedField[Widget, float64]",
		"IsActive orm.Field[Widget, bool]",
		"OpenedAt orm.TimeField[Widget]",
		"Attachment orm.BytesField[Widget]",
		// Decimal is the one kind whose struct/wire Go type (string) and
		// descriptor wrapper (OrderedField, for Gt/Lt/Between) diverge
		// from every other string-typed kind (Char/Text/UUID/Sequence).
		"Price orm.OrderedField[Widget, string]",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("WidgetFields missing %q:\n%s", want, src)
		}
	}

	if !strings.Contains(src, "var WidgetAllFields = []orm.AnyField[Widget]{") {
		t.Errorf("missing WidgetAllFields slice:\n%s", src)
	}
	for _, want := range []string{
		"WidgetFields.Name,", "WidgetFields.Code,", "WidgetFields.Quantity,",
		"WidgetFields.Weight,", "WidgetFields.IsActive,", "WidgetFields.OpenedAt,",
		"WidgetFields.Attachment,", "WidgetFields.Price,",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("WidgetAllFields missing entry %q:\n%s", want, src)
		}
	}
}

// Use int64 to isolate required-field scanning from integer narrowing; absent values keep
// zero values, while mismatched types error.
func TestRenderModelFile_ScanRequiredField(t *testing.T) {
	m := model.Define("widgets.widget").
		Field("quantity", model.BigInt().Required())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, `if v, ok := row["quantity"]; ok {`) {
		t.Errorf("required field's Scan block should not guard on v != nil:\n%s", src)
	}
	if !strings.Contains(src, `val, ok := v.(int64)`) {
		t.Errorf("missing int64 type assertion:\n%s", src)
	}
	if !strings.Contains(src, `return orm.NewDecodeError("Widget", "Quantity", "int64", v)`) {
		t.Errorf("missing DecodeError construction:\n%s", src)
	}
	if !strings.Contains(src, "x.Quantity = val") {
		t.Errorf("missing field assignment:\n%s", src)
	}
}

// Msgpack decodes integer values as int64 without preserving the encoder's int32 width;
// generated scanning must narrow after decoding.
func TestRenderModelFile_ScanIntegerFieldNarrowsFromInt64(t *testing.T) {
	m := model.Define("widgets.widget").
		Field("quantity", model.Integer().Required()).
		Field("optional_quantity", model.Integer())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, "Quantity int32 ") {
		t.Errorf("struct field should stay int32:\n%s", src)
	}
	if !strings.Contains(src, `val, ok := v.(int64)`) {
		t.Errorf("Scan should assert against int64, not int32:\n%s", src)
	}
	if !strings.Contains(src, `return orm.NewDecodeError("Widget", "Quantity", "int32", v)`) {
		t.Errorf("DecodeError should still name the struct field's own int32 type:\n%s", src)
	}
	if !strings.Contains(src, "x.Quantity = int32(val)") {
		t.Errorf("missing narrowing conversion for the required field:\n%s", src)
	}
	if !strings.Contains(src, "converted := int32(val)") || !strings.Contains(src, "x.OptionalQuantity = &converted") {
		t.Errorf("missing narrowing conversion for the optional field:\n%s", src)
	}
}

func TestRenderModelFile_ScanOptionalField(t *testing.T) {
	m := model.Define("widgets.widget").
		Field("nickname", model.Text())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, `if v, ok := row["nickname"]; ok && v != nil {`) {
		t.Errorf("optional field's Scan block should guard on v != nil:\n%s", src)
	}
	if !strings.Contains(src, `return orm.NewDecodeError("Widget", "Nickname", "*string", v)`) {
		t.Errorf("optional field's DecodeError should name the pointer type:\n%s", src)
	}
	if !strings.Contains(src, "x.Nickname = &val") {
		t.Errorf("missing pointer field assignment:\n%s", src)
	}
}

func TestRenderModelFile_ScanNamedTypeField(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("state", model.Selection("draft", "done").Required())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, `s, ok := v.(string)`) {
		t.Errorf("named-type field should assert against string:\n%s", src)
	}
	if !strings.Contains(src, "x.State = GadgetState(s)") {
		t.Errorf("missing named-type conversion:\n%s", src)
	}
}

func TestRenderModelFile_ScanRelationExpansion(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("customer_id", model.Many2One("contacts.contact").Required())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, `if v, ok := row["customer"]; ok && v != nil {`) {
		t.Errorf("missing relation expansion Scan guard:\n%s", src)
	}
	if !strings.Contains(src, "nested, ok := v.(map[string]any)") {
		t.Errorf("missing nested map assertion:\n%s", src)
	}
	if !strings.Contains(src, `ref.ID = id`) || !strings.Contains(src, "ref.DisplayName = dn") {
		t.Errorf("missing RelationRef field extraction:\n%s", src)
	}
	if !strings.Contains(src, "x.Customer = orm.Ref[ContactsContactRef]{RelationRef: &ref}") {
		t.Errorf("missing expansion field assignment:\n%s", src)
	}
	if strings.Contains(src, "GadgetFields.Customer,") {
		t.Errorf("Many2One expansion should not get an AllFields entry:\n%s", src)
	}
	if strings.Contains(src, "Customer orm.Field[") {
		t.Errorf("Many2One expansion should not get a field descriptor:\n%s", src)
	}
}

func TestRenderModelFile_ValuesBuilder_EmitsSetXPerWritableField(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("name", model.Text().Required()).
		Field("code", model.UUID().Required()).
		Field("quantity", model.Integer().Required()).
		Field("opened_at", model.TimestampTZ().Required()).
		Field("attachment", model.Bytea()).
		Field("state", model.Selection("draft", "done").Required())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, "type GadgetValues struct {") || !strings.Contains(src, "orm.Values[Gadget]") {
		t.Errorf("missing GadgetValues struct embedding orm.Values[Gadget]:\n%s", src)
	}
	if !strings.Contains(src, "func NewGadgetValues() *GadgetValues {") ||
		!strings.Contains(src, "return &GadgetValues{Values: *orm.NewValues[Gadget]()}") {
		t.Errorf("missing NewGadgetValues constructor:\n%s", src)
	}

	for _, want := range []string{
		// StringField — .Field required.
		"func (v *GadgetValues) SetName(x string) *GadgetValues {",
		"v.Values.Set(GadgetFields.Name.Field, x)",
		// plain Field — no .Field.
		"func (v *GadgetValues) SetCode(x string) *GadgetValues {",
		"v.Values.Set(GadgetFields.Code, x)",
		// OrderedField — .Field required.
		"func (v *GadgetValues) SetQuantity(x int32) *GadgetValues {",
		"v.Values.Set(GadgetFields.Quantity.Field, x)",
		// TimeField — .Field required.
		"func (v *GadgetValues) SetOpenedAt(x time.Time) *GadgetValues {",
		"v.Values.Set(GadgetFields.OpenedAt.Field, x)",
		// BytesField — orm.SetBytes, no .Field.
		"func (v *GadgetValues) SetAttachment(x []byte) *GadgetValues {",
		"v.Values.SetBytes(GadgetFields.Attachment, x)",
		// Selection's named type — plain Field, no .Field.
		"func (v *GadgetValues) SetState(x GadgetState) *GadgetValues {",
		"v.Values.Set(GadgetFields.State, x)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("output missing %q:\n%s", want, src)
		}
	}
}

func TestRenderModelFile_ValuesBuilder_SkipsReadonlyAndComputedFields(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("name", model.Text().Required()).
		Field("locked_note", model.Text().Readonly()).
		Field("total", model.Float().Computed("compute_total").Store(true))

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, "func (v *GadgetValues) SetName(x string) *GadgetValues {") {
		t.Errorf("writable field Name should still get a SetX method:\n%s", src)
	}
	if strings.Contains(src, "SetLockedNote") {
		t.Errorf("Readonly field should not get a SetX method:\n%s", src)
	}
	if strings.Contains(src, "SetTotal") {
		t.Errorf("Computed field should not get a SetX method:\n%s", src)
	}
}

func TestRenderModelFile_ValuesBuilder_Many2OneExpansionHasNoSetter(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("customer_id", model.Many2One("contacts.contact").Required())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, "func (v *GadgetValues) SetCustomerID(x string) *GadgetValues {") {
		t.Errorf("missing SetCustomerID for the Many2One FK field:\n%s", src)
	}
	if strings.Contains(src, "SetCustomer(") {
		t.Errorf("Many2One expansion should not get a SetX method:\n%s", src)
	}
}

func TestRenderModelFile_ValuesBuilder_DynamicLinkFieldsAreWritable(t *testing.T) {
	m := model.Define("widgets.link").
		Field("reference_type", model.Selection("sales.order", "contacts.contact").Required()).
		Field("source_id", model.DynamicLink("reference_type").Required()).
		Field("target_id", model.DynamicLink("reference_type").Required())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if n := strings.Count(src, "func (v *LinkValues) SetReferenceType("); n != 1 {
		t.Errorf("SetReferenceType emitted %d times, want exactly 1:\n%s", n, src)
	}
	if !strings.Contains(src, "func (v *LinkValues) SetSourceID(x string) *LinkValues {") {
		t.Errorf("missing SetSourceID:\n%s", src)
	}
	if !strings.Contains(src, "func (v *LinkValues) SetTargetID(x string) *LinkValues {") {
		t.Errorf("missing SetTargetID:\n%s", src)
	}
}

func TestRenderModelFile_QueryAndDelete(t *testing.T) {
	m := model.Define("widgets.gadget", model.Table("gadgets")).
		WithStandardFields()

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, "func (Gadget) Query() *orm.Query[Gadget] { return orm.From[Gadget]() }") {
		t.Errorf("missing Query() method:\n%s", src)
	}
	if !strings.Contains(src, "func (x Gadget) Delete() (orm.ExecResult, error) { return orm.Unlink[Gadget](x.ID) }") {
		t.Errorf("missing Delete() method:\n%s", src)
	}
	if !strings.Contains(src, "func (x Gadget) DeleteTx(tx *db.Tx) (orm.ExecResult, error) { return orm.UnlinkTx[Gadget](tx, x.ID) }") {
		t.Errorf("missing DeleteTx() method:\n%s", src)
	}
	if !strings.Contains(src, `"github.com/djangbahevans/goerp/sdk/go/db"`) {
		t.Errorf("output should import sdk/go/db for DeleteTx's *db.Tx parameter:\n%s", src)
	}
}

// Generated convenience methods share the struct namespace with fields; formatting alone
// does not detect their collisions.
func TestRenderModelFile_QueryFieldNameCollision_Errors(t *testing.T) {
	m := model.Define("widgets.saved_search").
		Field("query", model.Text().Required())

	if _, _, err := renderModelFile(m, nil, testGenContext()); err == nil {
		t.Fatal("expected an error for a field named \"query\" colliding with the generated Query() method")
	}
}

func TestRenderModelFile_DeleteFieldNameCollision_Errors(t *testing.T) {
	m := model.Define("widgets.gadget", model.Table("gadgets")).
		WithStandardFields().
		Field("delete", model.Boolean().Required())

	if _, _, err := renderModelFile(m, nil, testGenContext()); err == nil {
		t.Fatal("expected an error for a field named \"delete\" colliding with the generated Delete() method")
	}
}

func TestRenderModelFile_DeleteTxFieldNameCollision_Errors(t *testing.T) {
	m := model.Define("widgets.gadget", model.Table("gadgets")).
		WithStandardFields().
		Field("delete_tx", model.Boolean().Required())

	if _, _, err := renderModelFile(m, nil, testGenContext()); err == nil {
		t.Fatal("expected an error for a field named \"delete_tx\" colliding with the generated DeleteTx() method")
	}
}

// Delete requires one string primary key because orm.Unlink has no composite-ID argument.
func TestRenderModelFile_NoPrimaryKey_SkipsDelete(t *testing.T) {
	m := model.Define("widgets.widget").
		Field("name", model.Text().Required())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if !strings.Contains(src, "func (Widget) Query() *orm.Query[Widget] { return orm.From[Widget]() }") {
		t.Errorf("missing Query() method:\n%s", src)
	}
	if strings.Contains(src, "Delete()") {
		t.Errorf("model with no primary key should get no Delete():\n%s", src)
	}
}

func TestRenderModelFile_CompositePrimaryKey_SkipsDelete(t *testing.T) {
	m := model.Define("widgets.link").
		Field("left_id", model.UUID().PrimaryKey()).
		Field("right_id", model.UUID().PrimaryKey())

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	if strings.Contains(src, "Delete()") {
		t.Errorf("model with a composite primary key should get no Delete():\n%s", src)
	}
}

func TestRenderModelFile_ValueFields_DecoderPerKindAndNilForReadonly(t *testing.T) {
	m := model.Define("widgets.gadget").
		WithStandardFields().
		Field("name", model.Text().Required()).
		Field("quantity", model.Integer()).
		Field("opened_on", model.Date()).
		Field("opened_at", model.TimestampTZ()).
		Field("meta", model.JSONB()).
		Field("attachment", model.Bytea()).
		Field("state", model.Selection("draft", "done").Required()).
		Field("customer_id", model.Many2One("contacts.contact")).
		Field("total", model.Float().Computed("compute_total").Store(true))

	out, _, err := renderModelFile(m, nil, testGenContext())
	if err != nil {
		t.Fatalf("renderModelFile: %v", err)
	}
	src := normalizeSpaces(string(out))

	for _, want := range []string{
		"func (Gadget) ValueFields() map[string]orm.ValueDecoder {",
		`"id": nil,`,
		`"etag": nil,`,
		`"name": orm.DecodeValue[string],`,
		`"quantity": orm.DecodeValue[int32],`,
		`"opened_on": orm.DecodeDate,`,
		`"opened_at": orm.DecodeValue[time.Time],`,
		`"meta": orm.DecodeJSONB,`,
		`"attachment": orm.DecodeValue[[]byte],`,
		`"state": orm.DecodeValue[GadgetState],`,
		`"customer_id": orm.DecodeValue[string],`,
		`"total": nil,`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("output missing %q:\n%s", want, src)
		}
	}
	if strings.Contains(src, `"customer":`) {
		t.Errorf("Many2One expansion should not be a ValueFields entry:\n%s", src)
	}
}

func TestRenderModelFile_ValueFieldsMethodCollidesWithField_Errors(t *testing.T) {
	m := model.Define("widgets.gadget").
		Field("value_fields", model.Text())

	if _, _, err := renderModelFile(m, nil, testGenContext()); err == nil || !strings.Contains(err.Error(), "ValueFields") {
		t.Fatalf("renderModelFile error = %v, want a ValueFields name collision", err)
	}
}
