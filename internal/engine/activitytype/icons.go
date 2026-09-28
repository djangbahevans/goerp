package activitytype

import (
	_ "embed"
	"strings"
)

// lucideIconsFile is every icon name lucide-react's dynamicIconImports
// exports, one per line, at the version shell/package-lock.json locks
// (1.42.0) — the names the shell's Icon component can render. After a
// lucide-react upgrade, regenerate it from the shell's install:
//
//	grep -oE '^  "?[a-z0-9-]+"?: \(' shell/node_modules/lucide-react/dist/esm/dynamicIconImports.mjs \
//	  | sed -E 's/^  "?([a-z0-9-]+)"?: \(/\1/' | sort -u > internal/engine/activitytype/lucide_icons.txt
//
//go:embed lucide_icons.txt
var lucideIconsFile string

var lucideIcons = func() map[string]struct{} {
	set := map[string]struct{}{}
	for name := range strings.Lines(lucideIconsFile) {
		if name = strings.TrimSpace(name); name != "" {
			set[name] = struct{}{}
		}
	}
	return set
}()

// ValidIcon reports whether name is a Lucide icon the shell can render.
func ValidIcon(name string) bool {
	_, ok := lucideIcons[name]
	return ok
}
