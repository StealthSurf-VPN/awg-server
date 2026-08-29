package config

import (
	"encoding/json"
	"testing"
)

func TestParseUint16Range(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		want       string
		wantScalar uint16
		isScalar   bool
	}{
		{name: "zero", input: "0", want: "0", wantScalar: 0, isScalar: true},
		{name: "scalar", input: "25", want: "25", wantScalar: 25, isScalar: true},
		{name: "equal range", input: "25-25", want: "25"},
		{name: "range", input: "25-35", want: "25-35"},
		{name: "maximum scalar", input: "65535", want: "65535", wantScalar: 65535, isScalar: true},
		{name: "off alias", input: "off", want: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUint16Range(tt.input)
			if err != nil {
				t.Fatalf("ParseUint16Range(%q) error = %v", tt.input, err)
			}
			if got.String() != tt.want {
				t.Fatalf("ParseUint16Range(%q).String() = %q, want %q", tt.input, got.String(), tt.want)
			}
			if got.IsScalar() != tt.isScalar {
				t.Fatalf("ParseUint16Range(%q).IsScalar() = %t, want %t", tt.input, got.IsScalar(), tt.isScalar)
			}

			scalar, ok := got.Scalar()
			if ok != tt.isScalar || scalar != tt.wantScalar {
				t.Fatalf("ParseUint16Range(%q).Scalar() = (%d, %t), want (%d, %t)", tt.input, scalar, ok, tt.wantScalar, tt.isScalar)
			}
		})
	}
}

func TestUint16RangeJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		json  string
	}{
		{name: "integer scalar", input: "0", want: "0", json: "0"},
		{name: "quoted scalar", input: `"25"`, want: "25", json: "25"},
		{name: "quoted equal range", input: `"25-25"`, want: "25", json: "25"},
		{name: "quoted range", input: `"25-35"`, want: "25-35", json: `"25-35"`},
		{name: "off alias", input: `"off"`, want: "0", json: "0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got Uint16Range
			if err := json.Unmarshal([]byte(tt.input), &got); err != nil {
				t.Fatalf("json.Unmarshal(%s) error = %v", tt.input, err)
			}
			if got.String() != tt.want {
				t.Fatalf("json.Unmarshal(%s).String() = %q, want %q", tt.input, got.String(), tt.want)
			}

			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			if string(encoded) != tt.json {
				t.Fatalf("json.Marshal() = %s, want %s", encoded, tt.json)
			}
		})
	}
}

func TestUint16RangeRejectsMalformedValues(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "negative", input: "-1"},
		{name: "float", input: "1.5"},
		{name: "exponent", input: "1e2"},
		{name: "leading whitespace", input: " 25"},
		{name: "trailing whitespace", input: "25 "},
		{name: "empty", input: ""},
		{name: "reversed range", input: "35-25"},
		{name: "above uint16", input: "65536"},
		{name: "multiple dashes", input: "1-2-3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseUint16Range(tt.input); err == nil {
				t.Fatalf("ParseUint16Range(%q) error = nil", tt.input)
			}
		})
	}

	jsonInputs := []string{
		`"35-25"`,
		"65536",
		"null",
	}

	for _, input := range jsonInputs {
		t.Run("json "+input, func(t *testing.T) {
			var got Uint16Range
			if err := json.Unmarshal([]byte(input), &got); err == nil {
				t.Fatalf("json.Unmarshal(%s) error = nil", input)
			}
		})
	}
}
