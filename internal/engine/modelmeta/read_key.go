// Package modelmeta defines metadata shared by model schema and record expansion.
package modelmeta

import "strings"

// ReadKey returns the expanded record key for a relation-like field, or an
// empty string for a scalar or inverse relation.
func ReadKey(name, kind string) string {
	switch kind {
	case "many2one", "dynamic_link", "file":
		return strings.TrimSuffix(name, "_id")
	case "many2many", "file_multi":
		if stem, ok := strings.CutSuffix(name, "_ids"); ok {
			return stem + "s"
		}
		return name
	default:
		return ""
	}
}
