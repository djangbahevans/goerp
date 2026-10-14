package modelmeta

import "testing"

func TestReadKey(t *testing.T) {
	tests := []struct {
		name string
		kind string
		want string
	}{
		{name: "customer_id", kind: "many2one", want: "customer"},
		{name: "tag_ids", kind: "many2many", want: "tags"},
		{name: "reference_id", kind: "dynamic_link", want: "reference"},
		{name: "signed_pdf_id", kind: "file", want: "signed_pdf"},
		{name: "attachment_ids", kind: "file_multi", want: "attachments"},
		{name: "owner", kind: "many2one", want: "owner"},
		{name: "members", kind: "many2many", want: "members"},
		{name: "external_id", kind: "text"},
		{name: "line_ids", kind: "one2many"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReadKey(tt.name, tt.kind); got != tt.want {
				t.Errorf("ReadKey(%q, %q) = %q, want %q", tt.name, tt.kind, got, tt.want)
			}
		})
	}
}
