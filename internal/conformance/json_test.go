package conformance

import (
	"encoding/json"
	"testing"

	"github.com/nslaughter/financial-data-api/internal/expected"
)

// jsonValue decodes text as the runner decodes JSON, with json.Number.
func jsonValue(t *testing.T, text string) any {
	t.Helper()
	var v any
	if err := expected.Decode([]byte(text), &v); err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	return v
}

func TestDecimal(t *testing.T) {
	tests := []struct{ in, want string }{
		{"36", "36"},
		{"36.0", "36"},
		{"3.6e1", "36"},
		{"3.6E+1", "36"},
		{"3600e-2", "36"},
		{"0.1e1", "1"},
		{"1e3", "1000"},
		{"-0", "0"},
		{"-0.0e5", "0"},
		{"1.50", "1.5"},
		{"15e-1", "1.5"},
		{"1.5e-3", "0.0015"},
		{"-2.5E+0", "-2.5"},
		{"123456789012345678901234567890", "123456789012345678901234567890"},
	}
	for _, tt := range tests {
		got, err := decimal(json.Number(tt.in))
		if err != nil || got != tt.want {
			t.Errorf("decimal(%s) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	if _, err := decimal("1e2000000"); err == nil {
		t.Error("decimal(1e2000000): want an error")
	}
}
