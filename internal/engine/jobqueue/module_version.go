package jobqueue

import (
	"encoding/hex"
	"fmt"
	"strings"
)

const moduleVersionTagPrefix = "module_version-"

func ModuleVersionTag(version string) string {
	// River tags exclude the punctuation used by semantic versions.
	return moduleVersionTagPrefix + hex.EncodeToString([]byte(version))
}

func ParseModuleVersionTag(tags []string) (string, error) {
	for _, tag := range tags {
		if encoded, ok := strings.CutPrefix(tag, moduleVersionTagPrefix); ok {
			version, err := hex.DecodeString(encoded)
			if err != nil || len(version) == 0 {
				return "", fmt.Errorf("invalid module version tag %q", tag)
			}
			return string(version), nil
		}
	}

	return "", nil
}
