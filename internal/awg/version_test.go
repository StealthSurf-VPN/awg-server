package awg

import "testing"

func TestParseProtocolVersion(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  ProtocolVersion
	}{
		{name: "legacy alias", input: "2", want: ProtocolVersion2},
		{name: "legacy canonical", input: "2.0", want: ProtocolVersion2},
		{name: "awg 3.1", input: "3.1", want: ProtocolVersion31},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseProtocolVersion(tt.input)
			if err != nil {
				t.Fatalf("ParseProtocolVersion(%q) error = %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("ParseProtocolVersion(%q) = %q, want %q", tt.input, got, tt.want)
			}
			if !got.Valid() {
				t.Fatalf("ParseProtocolVersion(%q).Valid() = false", tt.input)
			}
			if got.String() != string(tt.want) {
				t.Fatalf("ParseProtocolVersion(%q).String() = %q, want %q", tt.input, got.String(), tt.want)
			}
		})
	}
}

func TestParseProtocolVersionRejectsUnknownValues(t *testing.T) {
	for _, input := range []string{"", "3", " 2.0"} {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseProtocolVersion(input); err == nil {
				t.Fatalf("ParseProtocolVersion(%q) error = nil", input)
			}
		})
	}

	if ProtocolVersion("2").Valid() {
		t.Fatal("ProtocolVersion(\"2\").Valid() = true")
	}
}
