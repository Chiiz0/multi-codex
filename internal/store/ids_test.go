package store

import "testing"

func TestValidUUIDText(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "default seed", value: defaultProjectID, want: true},
		{name: "uppercase", value: "00000000-0000-7000-8000-ABCDEFABCDEF", want: true},
		{name: "legacy id", value: "proj_demo", want: false},
		{name: "missing hyphen", value: "000000000000-7000-8000-000000000101", want: false},
		{name: "non hex", value: "00000000-0000-7000-8000-00000000010z", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validUUIDText(tt.value); got != tt.want {
				t.Fatalf("validUUIDText(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}
