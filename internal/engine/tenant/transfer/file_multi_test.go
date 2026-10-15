package transfer

import (
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func TestFileMultiTransferOwnershipAndSelection(t *testing.T) {
	md := model.Define("order").Field("id", model.UUID().PrimaryKey()).
		Field("attachment_ids", model.FileMulti("order_attachments")).
		Field("private_ids", model.FileMulti("private_files").Access(model.AccessRead(perm.Ref("sales:order:private")))).
		Field("extra_ids", model.FileMulti("extra_files"))
	md.Fields[3].DeclaringModule = "industry"
	relations := Relations("sales", []model.ModelDeclaration{*md})
	included, omitted := SelectRelations(relations, []string{"sales", "industry"})
	if len(included) != 2 || len(omitted) != 0 {
		t.Fatalf("relations=%+v omissions=%+v", included, omitted)
	}
	for _, relation := range included {
		if relation.SourceModule != "sales" || relation.TargetModule != "sales" || relation.Column1 != "order_id" || relation.Column2 != "file_id" {
			t.Fatalf("file relation: %+v", relation)
		}
	}
	included, omitted = SelectRelations(relations, []string{"industry"})
	if len(included) != 0 || len(omitted) != 1 || omitted[0].MissingModules[0] != "sales" {
		t.Fatalf("excluded owner: %+v, %+v", included, omitted)
	}
}
