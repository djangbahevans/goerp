package modeltest

// FieldOpts describes the declared kind and relation target expected by AssertField.
type FieldOpts struct {
	Type         string
	RelatedModel string
}

// TestModel inspects the module's declared models without querying tenant data.
type TestModel struct {
	h *Harness
}

// AssertField fails the test unless the named field has the expected kind and target.
// qualifiedModel is a module-qualified model name such as "hr.employee".
func (m *TestModel) AssertField(qualifiedModel, field string, want FieldOpts) {
	m.h.t.Helper()
	for _, md := range m.h.module.ModelDecls {
		if md.QualifiedName(m.h.moduleName) != qualifiedModel {
			continue
		}
		for _, f := range md.Fields {
			if f.Name == field {
				if f.Def.Kind.String() != want.Type || f.Def.RelatedModel != want.RelatedModel {
					m.h.t.Errorf("%s.%s = %s(%q), want %s(%q)", qualifiedModel, field, f.Def.Kind, f.Def.RelatedModel, want.Type, want.RelatedModel)
				}
				return
			}
		}
		m.h.t.Errorf("model %s has no field %s", qualifiedModel, field)
		return
	}
	m.h.t.Errorf("model %s is not declared", qualifiedModel)
}
