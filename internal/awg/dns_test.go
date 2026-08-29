package awg

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

func TestNormalizeOverridesForVersionClonesRangesAndDNSServers(t *testing.T) {
	params := &AWGParams{
		DNSMode:                DNSModeCustom,
		DNSServers:             []string{"1.1.1.1", "1.0.0.1"},
		PersistentKeepalive:    rangePointer(t, "25-35"),
		ContentPaddingAddition: rangePointer(t, "10-100"),
		RekeyAfterTime:         rangePointer(t, "100-120"),
		RekeyTimeout:           rangePointer(t, "3-7"),
		RejectAfterTime:        rangePointer(t, "150-180"),
		KeepaliveTimeout:       rangePointer(t, "5-15"),
		MaxHandshakeAttempts:   rangePointer(t, "15-20"),
		RandomTrailers:         "on",
		DisableCookies:         "off",
	}

	normalized, err := NormalizeOverridesForVersion(ProtocolVersion31, params)
	if err != nil {
		t.Fatalf("NormalizeOverridesForVersion() error = %v", err)
	}
	if normalized == params {
		t.Fatal("NormalizeOverridesForVersion() returned the input pointer")
	}
	if &normalized.DNSServers[0] == &params.DNSServers[0] {
		t.Fatal("NormalizeOverridesForVersion() did not clone DNS servers")
	}

	ranges := []struct {
		name string
		got  any
		want any
	}{
		{name: "persistent keepalive", got: normalized.PersistentKeepalive, want: params.PersistentKeepalive},
		{name: "content padding", got: normalized.ContentPaddingAddition, want: params.ContentPaddingAddition},
		{name: "rekey after", got: normalized.RekeyAfterTime, want: params.RekeyAfterTime},
		{name: "rekey timeout", got: normalized.RekeyTimeout, want: params.RekeyTimeout},
		{name: "reject after", got: normalized.RejectAfterTime, want: params.RejectAfterTime},
		{name: "keepalive timeout", got: normalized.KeepaliveTimeout, want: params.KeepaliveTimeout},
		{name: "max handshake attempts", got: normalized.MaxHandshakeAttempts, want: params.MaxHandshakeAttempts},
	}

	for _, tt := range ranges {
		if tt.got == tt.want {
			t.Fatalf("NormalizeOverridesForVersion() did not clone %s", tt.name)
		}
	}
}

func TestNormalizeOverridesForVersionCanonicalizesRangesAfterValidation(t *testing.T) {
	params := &AWGParams{
		PersistentKeepalive:    rangePointer(t, "off"),
		ContentPaddingAddition: rangePointer(t, "25-25"),
		RekeyAfterTime:         rangePointer(t, "off"),
		RekeyTimeout:           rangePointer(t, "25-25"),
		RejectAfterTime:        rangePointer(t, "off"),
		KeepaliveTimeout:       rangePointer(t, "25-25"),
		MaxHandshakeAttempts:   rangePointer(t, "off"),
	}

	if _, err := NormalizeOverridesForVersion(ProtocolVersion2, params); err == nil {
		t.Fatal("NormalizeOverridesForVersion(2.0) accepted non-scalar input syntax")
	}
	if params.PersistentKeepalive.IsCanonical() || params.ContentPaddingAddition.IsCanonical() {
		t.Fatal("failed legacy normalization mutated the input")
	}

	normalized, err := NormalizeOverridesForVersion(ProtocolVersion31, params)
	if err != nil {
		t.Fatalf("NormalizeOverridesForVersion(3.1) error = %v", err)
	}
	for name, tt := range map[string]struct {
		value *config.Uint16Range
		want  uint16
	}{
		"persistent keepalive":   {value: normalized.PersistentKeepalive, want: 0},
		"content padding":        {value: normalized.ContentPaddingAddition, want: 25},
		"rekey after time":       {value: normalized.RekeyAfterTime, want: 0},
		"rekey timeout":          {value: normalized.RekeyTimeout, want: 25},
		"reject after time":      {value: normalized.RejectAfterTime, want: 0},
		"keepalive timeout":      {value: normalized.KeepaliveTimeout, want: 25},
		"max handshake attempts": {value: normalized.MaxHandshakeAttempts, want: 0},
	} {
		if !tt.value.IsCanonical() || !tt.value.IsScalar() {
			t.Fatalf("normalized %s retained non-canonical syntax", name)
		}
		if scalar, _ := tt.value.Scalar(); scalar != tt.want {
			t.Fatalf("normalized %s = %d, want %d", name, scalar, tt.want)
		}
	}
}

func TestAWGParamsUnmarshalJSONRejectsExplicitNullForRangeAndToggleFields(t *testing.T) {
	fields := []string{
		"persistent_keepalive",
		"content_padding_addition",
		"rekey_after_time",
		"rekey_timeout",
		"reject_after_time",
		"keepalive_timeout",
		"max_handshake_attempts",
		"random_trailers",
		"disable_cookies",
	}

	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			var params AWGParams
			payload := []byte(`{"` + strings.ToUpper(field) + `":null}`)

			err := json.Unmarshal(payload, &params)
			if err == nil {
				t.Fatal("Unmarshal() error = nil, want explicit null rejection")
			}
			if !errors.Is(err, ErrInvalidParams) {
				t.Fatalf("Unmarshal() error = %v, want ErrInvalidParams", err)
			}
		})
	}
}

func TestAWGParamsUnmarshalJSONKeepsTopLevelNullAndUnknownFieldsCompatible(t *testing.T) {
	params := AWGParams{RandomTrailers: "on"}
	if err := json.Unmarshal([]byte("null"), &params); err != nil {
		t.Fatalf("Unmarshal(top-level null) error = %v", err)
	}
	if params.RandomTrailers != "" {
		t.Fatalf("Unmarshal(top-level null) RandomTrailers = %q, want empty", params.RandomTrailers)
	}

	if err := json.Unmarshal([]byte(`{"unknown_field":"ignored"}`), &params); err != nil {
		t.Fatalf("Unmarshal(unknown field) error = %v", err)
	}
}
