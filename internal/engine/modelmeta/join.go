package modelmeta

import "github.com/djangbahevans/goerp/sdk/go/model"

func IsJoinRelation(kind model.FieldKind) bool {
	return kind == model.KindMany2Many || kind == model.KindFileMulti
}

func JoinDefinition(md model.ModelDeclaration, f model.NamedField) model.FieldDef {
	d := f.Def
	if d.Kind == model.KindFileMulti {
		d.Column1 = md.ResourceName() + "_id"
		d.Column2 = "file_id"
	}
	return d
}
