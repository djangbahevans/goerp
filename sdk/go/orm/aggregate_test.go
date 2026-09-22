package orm

import "testing"

func TestToInt64(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want int64
	}{
		{"nil", nil, 0},
		{"int64", int64(42), 42},
		{"int", 7, 7},
		{"float64", float64(9), 9},
		{"numeric string", "123", 123},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toInt64(tt.in)
			if err != nil {
				t.Fatalf("toInt64(%v): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("toInt64(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestToInt64_UnexpectedTypeErrors(t *testing.T) {
	if _, err := toInt64(true); err == nil {
		t.Error("expected an error for a non-numeric type")
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want float64
	}{
		{"nil", nil, 0},
		{"float64", 12.5, 12.5},
		{"float32", float32(1.5), 1.5},
		{"int64", int64(4), 4},
		{"int", 4, 4},
		{"decimal string", "266.6667", 266.6667},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := toFloat64(tt.in)
			if err != nil {
				t.Fatalf("toFloat64(%v): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("toFloat64(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestToFloat64_UnexpectedTypeErrors(t *testing.T) {
	if _, err := toFloat64(true); err == nil {
		t.Error("expected an error for a non-numeric type")
	}
}
