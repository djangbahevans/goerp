package jobqueue

import "testing"

func TestParseModuleVersionTag(t *testing.T) {
	for _, tt := range []struct {
		name    string
		tags    []string
		want    string
		wantErr bool
	}{
		{name: "untagged"},
		{name: "unrelated tags", tags: []string{"priority:high", "other_module_version:1.0.0"}},
		{name: "version tag", tags: []string{"priority-high", "module_version-312e322e33"}, want: "1.2.3"},
		{name: "prerelease and build metadata", tags: []string{ModuleVersionTag("1.2.3-rc.1+build.2")}, want: "1.2.3-rc.1+build.2"},
		{name: "malformed pin", tags: []string{"module_version-xyz"}, wantErr: true},
		{name: "empty pin", tags: []string{"module_version-"}, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := ParseModuleVersionTag(tt.tags); got != tt.want || (err != nil) != tt.wantErr {
				t.Fatalf("ParseModuleVersionTag() = %q, %v; want %q, error %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}
