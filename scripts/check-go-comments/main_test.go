package main

import "testing"

func TestCheckComments(t *testing.T) {
	tests := []struct {
		name string
		path string
		src  string
		want int
	}{
		{
			name: "tracker in line comment",
			path: "internal/engine/example.go",
			src:  "package example\n// See goerp#123.\nvar Value int\n",
			want: 1,
		},
		{
			name: "tracker in block comment",
			path: "cmd/example/main.go",
			src:  "package main\n/* PR #123 describes this behavior. */\n",
			want: 1,
		},
		{
			name: "tracker in inline comment",
			path: "modules/example/main.go",
			src:  "package example\nvar Value int // (#123)\n",
			want: 1,
		},
		{
			name: "tracker split across lines",
			path: "internal/module/example_test.go",
			src:  "package example\n// The behavior follows issue\n// #123.\n",
			want: 1,
		},
		{
			name: "tracker link",
			path: "internal/example.go",
			src:  "package example\n// https://github.com/owner/repo/pull/123\n",
			want: 1,
		},
		{
			name: "tracker without hash",
			path: "internal/example.go",
			src:  "package example\n// Issue 123 defines this behavior.\n",
			want: 1,
		},
		{
			name: "tracker identifier",
			path: "internal/example.go",
			src:  "package example\n// See goerp-123.\n",
			want: 1,
		},
		{
			name: "tracker number split across lines",
			path: "internal/example.go",
			src:  "package example\n// Issue #\n// 123 defines this behavior.\n",
			want: 1,
		},
		{
			name: "tracker in compiler directive",
			path: "internal/example.go",
			src:  "package example\n//go:generate echo goerp#123\n",
			want: 1,
		},
		{
			name: "SDK document citation",
			path: "sdk/go/example/example.go",
			src:  "package example\n// See host-abi-reference.md for details.\n",
			want: 1,
		},
		{
			name: "ABI document citation",
			path: "contract/abi/v1/example.go",
			src:  "package abi\n/* See host-abi-reference.md for details. */\n",
			want: 1,
		},
		{
			name: "ABI section citation",
			path: "contract/abi/v1/example.go",
			src:  "package abi\nvar Value int // §3 defines this field.\n",
			want: 1,
		},
		{
			name: "ABI engine path",
			path: "contract/abi/v1/example.go",
			src:  "package abi\n// See internal/engine/example.go.\n",
			want: 1,
		},
		{
			name: "engine citation supplements explanation",
			path: "internal/engine/example.go",
			src:  "package example\n// Wait for commit before delivery (event-system.md §8).\n",
		},
		{
			name: "API behavior is self-contained",
			path: "contract/abi/v1/example.go",
			src:  "package abi\n// Zero TTL means the entry never expires.\n",
		},
		{
			name: "comment-like string",
			path: "sdk/go/example/example.go",
			src:  "package example\nconst Value = `// goerp#123 host-abi-reference.md §3 internal/engine/example.go`\n",
		},
		{
			name: "testdata exempt",
			path: "internal/engine/testdata/example/main.go",
			src:  "package main\n// goerp#123\n",
		},
		{
			name: "top-level testdata exempt",
			path: "testdata/example/main.go",
			src:  "package main\n// goerp#123\n",
		},
		{
			name: "testdata substring is not exempt",
			path: "internal/testdatabase/example.go",
			src:  "package example\n// goerp#123\n",
			want: 1,
		},
		{
			name: "compiler directives preserved",
			path: "sdk/go/example/example.go",
			src:  "//go:build wasip1\n\npackage example\n//go:wasmimport goerp.host host_db_query\nfunc query()\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings, err := checkComments(tt.path, []byte(tt.src))
			if err != nil {
				t.Fatal(err)
			}

			if len(findings) != tt.want {
				t.Fatalf("findings = %v, want %d", findings, tt.want)
			}
		})
	}
}

func TestCheckComments_InvalidSource(t *testing.T) {
	if _, err := checkComments("internal/example.go", []byte("package")); err == nil {
		t.Fatal("invalid Go source must fail the check")
	}
}
