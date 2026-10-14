package orm

import "fmt"

// RelatedRecord retains an expanded record from a separately built module.
// T identifies its target resource; Fields contains the permitted stored fields.
type RelatedRecord[T Model] struct {
	Fields map[string]any
}

// Scan retains the related record's permitted fields.
func (r *RelatedRecord[T]) Scan(row map[string]any) error {
	r.Fields = row
	return nil
}

// DecodeIDArray decodes a Many2Many field's bare IDs from a host response.
func DecodeIDArray(value any) ([]string, error) {
	if ids, ok := value.([]string); ok {
		return ids, nil
	}

	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("orm: expected an ID array, got %T", value)
	}
	ids := make([]string, len(items))
	for i, item := range items {
		id, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("orm: expected string ID at index %d, got %T", i, item)
		}
		ids[i] = id
	}
	return ids, nil
}

// DecodeRecordArray scans expanded related objects into generated records.
// Both direct engine results and MessagePack-decoded host results are accepted.
func DecodeRecordArray[T any, PT ptrScanner[T]](value any) ([]T, error) {
	if records, ok := value.([]map[string]any); ok {
		return decodeRecords[T, PT](records)
	}

	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("orm: expected a record array, got %T", value)
	}
	records := make([]map[string]any, len(items))
	for i, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("orm: expected record at index %d, got %T", i, item)
		}
		records[i] = row
	}
	return decodeRecords[T, PT](records)
}
