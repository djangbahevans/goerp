package def

import "fmt"

// ValidateFieldType rejects incompatible widgets and static selections without
// options. An empty fieldType selects the storage type's default widget.
func ValidateFieldType(typ, fieldType string, hasOptions bool) error {
	if fieldType == "" {
		return nil
	}

	compatible := false
	switch fieldType {
	case "text":
		compatible = typ == TypeString || typ == TypeDuration
	case "email", "url", "phone", "textarea", "password", "country_select", "language_select", "timezone_select", "currency_select", "color_picker", "icon_picker":
		compatible = typ == TypeString
	case "number":
		compatible = typ == TypeInteger || typ == TypeFloat
	case "integer":
		compatible = typ == TypeInteger
	case "boolean", "toggle":
		compatible = typ == TypeBoolean
	case "duration":
		compatible = typ == TypeDuration
	case "json":
		compatible = typ == TypeJSON
	case "select", "radio":
		compatible = typ == TypeString || typ == TypeInteger || typ == TypeFloat
	case "multi_select", "multiselect":
		compatible = typ == TypeStringList || typ == TypeIntegerList || typ == TypeFloatList
	default:
		return fmt.Errorf("unsupported settings field_type %q", fieldType)
	}

	if !compatible {
		return fmt.Errorf("field_type %q is incompatible with type %q", fieldType, typ)
	}

	switch fieldType {
	case "select", "radio", "multi_select", "multiselect":
		if !hasOptions {
			return fmt.Errorf("field_type %q requires non-empty options", fieldType)
		}
	}

	return nil
}
