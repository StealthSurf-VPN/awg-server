package awg

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func validTestProfile() AWGParams {
	return AWGParams{
		Jc: 5, Jmin: 50, Jmax: 1000,
		S1: 15, S2: 72,
		H1: "1-2", H2: "3-4", H3: "5-6", H4: "7-8",
	}
}

func TestValidateProfile(t *testing.T) {
	valid := validTestProfile()

	missingJmin := valid
	missingJmin.Jmin = 0
	missingJmax := valid
	missingJmax.Jmax = 0
	invalidJRange := valid
	invalidJRange.Jmin = invalidJRange.Jmax
	invalidS2 := valid
	invalidS2.S2 = invalidS2.S1 + 56
	missingHeader := valid
	missingHeader.H4 = ""
	overlappingHeaders := valid
	overlappingHeaders.H2 = "2-3"
	reversedHeader := valid
	reversedHeader.H1 = "2-1"

	tests := []struct {
		name      string
		params    AWGParams
		wantField string
	}{
		{name: "valid", params: valid},
		{name: "jc requires jmin", params: missingJmin, wantField: "jmin"},
		{name: "jc requires jmax", params: missingJmax, wantField: "jmax"},
		{name: "jmin below jmax", params: invalidJRange, wantField: "jmin"},
		{name: "s2 differs from s1 plus 56", params: invalidS2, wantField: "s2"},
		{name: "all headers required", params: missingHeader, wantField: "h4"},
		{name: "headers do not overlap", params: overlappingHeaders, wantField: "h2"},
		{name: "header range is ordered", params: reversedHeader, wantField: "h1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProfile(tt.params)
			assertValidationField(t, err, tt.wantField)
		})
	}
}

func TestValidateOverridesBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		params    AWGParams
		wantField string
	}{
		{
			name: "accepted boundaries",
			params: AWGParams{
				Port: 1024, ClientListenPort: 65535, MTU: 1280,
				PersistentKeepalive: rangePointer(t, "0"),
				Jc:                  128, Jmin: 1280, Jmax: 1280,
				S1: 1132, S2: 1188, S3: 64, S4: 32,
				H1: "4294967295", I1: "<b 0x00ff><t><r 10><rc 0><rd 1000>",
			},
		},
		{name: "port below range", params: AWGParams{Port: 1023}, wantField: "port"},
		{name: "client port above range", params: AWGParams{ClientListenPort: 65536}, wantField: "client_listen_port"},
		{name: "mtu below range", params: AWGParams{MTU: 1279}, wantField: "mtu"},
		{name: "jc above range", params: AWGParams{Jc: 129}, wantField: "jc"},
		{name: "s4 above range", params: AWGParams{S4: 33}, wantField: "s4"},
		{name: "header above uint32", params: AWGParams{H1: "4294967296"}, wantField: "h1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOverrides(&tt.params)
			assertValidationField(t, err, tt.wantField)
		})
	}
}

func TestValidateOverridesForVersion(t *testing.T) {
	legacyFields := []struct {
		name      string
		params    AWGParams
		wantField string
	}{
		{name: "content padding", params: AWGParams{ContentPaddingAddition: rangePointer(t, "10-100")}, wantField: "content_padding_addition"},
		{name: "rekey after", params: AWGParams{RekeyAfterTime: rangePointer(t, "100-120")}, wantField: "rekey_after_time"},
		{name: "rekey timeout", params: AWGParams{RekeyTimeout: rangePointer(t, "3-7")}, wantField: "rekey_timeout"},
		{name: "reject after", params: AWGParams{RejectAfterTime: rangePointer(t, "150-180")}, wantField: "reject_after_time"},
		{name: "keepalive timeout", params: AWGParams{KeepaliveTimeout: rangePointer(t, "5-15")}, wantField: "keepalive_timeout"},
		{name: "max handshake attempts", params: AWGParams{MaxHandshakeAttempts: rangePointer(t, "15-20")}, wantField: "max_handshake_attempts"},
		{name: "random trailers", params: AWGParams{RandomTrailers: "on"}, wantField: "random_trailers"},
		{name: "disable cookies", params: AWGParams{DisableCookies: "off"}, wantField: "disable_cookies"},
		{name: "ranged keepalive", params: AWGParams{PersistentKeepalive: rangePointer(t, "25-35")}, wantField: "persistent_keepalive"},
		{name: "equal ranged keepalive", params: AWGParams{PersistentKeepalive: rangePointer(t, "25-25")}, wantField: "persistent_keepalive"},
		{name: "off keepalive", params: AWGParams{PersistentKeepalive: rangePointer(t, "off")}, wantField: "persistent_keepalive"},
	}

	for _, tt := range legacyFields {
		t.Run("2.0 rejects "+tt.name, func(t *testing.T) {
			err := ValidateOverridesForVersion(ProtocolVersion2, &tt.params)
			assertValidationField(t, err, tt.wantField)
		})
	}

	v31 := AWGParams{
		PersistentKeepalive:    rangePointer(t, "off"),
		ContentPaddingAddition: rangePointer(t, "10-100"),
		RekeyAfterTime:         rangePointer(t, "100-120"),
		RekeyTimeout:           rangePointer(t, "3-7"),
		RejectAfterTime:        rangePointer(t, "150-180"),
		KeepaliveTimeout:       rangePointer(t, "5-15"),
		MaxHandshakeAttempts:   rangePointer(t, "15-20"),
		RandomTrailers:         "on",
		DisableCookies:         "off",
	}
	if err := ValidateOverridesForVersion(ProtocolVersion31, &v31); err != nil {
		t.Fatalf("ValidateOverridesForVersion(3.1) error = %v", err)
	}

	invalidToggles := []struct {
		name      string
		params    AWGParams
		wantField string
	}{
		{name: "random trailers", params: AWGParams{RandomTrailers: "yes"}, wantField: "random_trailers"},
		{name: "disable cookies", params: AWGParams{DisableCookies: "false"}, wantField: "disable_cookies"},
	}

	for _, tt := range invalidToggles {
		t.Run("3.1 rejects invalid "+tt.name, func(t *testing.T) {
			err := ValidateOverridesForVersion(ProtocolVersion31, &tt.params)
			assertValidationField(t, err, tt.wantField)
		})
	}
}

func TestValidateProfileForVersionRequiresAWG31PaddingMinimum(t *testing.T) {
	params := validTestProfile()
	params.S3 = 11
	params.S4 = 12

	err := ValidateProfileForVersion(ProtocolVersion31, params)
	assertValidationField(t, err, "s3")
}

func TestValidateCPSTags(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		wantField string
	}{
		{name: "supported tags", value: "<b 0x00ff><t><r 10><rc 0><rd 1000>"},
		{name: "text outside tag", value: "prefix<t>", wantField: "i1"},
		{name: "unterminated tag", value: "<t", wantField: "i1"},
		{name: "unsupported tag", value: "<x 1>", wantField: "i1"},
		{name: "odd static hex", value: "<b 0xfff>", wantField: "i1"},
		{name: "tag above size limit", value: "<r 1001>", wantField: "i1"},
		{name: "expanded packet above limit", value: "<r 1000><rd 281>", wantField: "i1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOverrides(&AWGParams{I1: tt.value})
			assertValidationField(t, err, tt.wantField)
		})
	}
}

func TestNormalizeOverridesDNS(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		want        *AWGParams
		wantField   string
		resolvedDNS string
		includeDNS  bool
	}{
		{name: "empty object", input: `{}`, includeDNS: true, resolvedDNS: "8.8.8.8"},
		{name: "legacy", input: `{"dns":"9.9.9.9"}`, want: &AWGParams{DNS: "9.9.9.9"}, includeDNS: true, resolvedDNS: "9.9.9.9"},
		{
			name:       "custom deduplicates stably",
			input:      `{"dns_mode":"custom","dns_servers":["1.1.1.1","1.0.0.1","1.1.1.1"]}`,
			want:       &AWGParams{DNSMode: DNSModeCustom, DNSServers: []string{"1.1.1.1", "1.0.0.1"}},
			includeDNS: true, resolvedDNS: "1.1.1.1, 1.0.0.1",
		},
		{name: "system omits dns", input: `{"dns_mode":"system"}`, want: &AWGParams{DNSMode: DNSModeSystem}},
		{name: "default inherits", input: `{"dns_mode":"default"}`, want: &AWGParams{DNSMode: DNSModeDefault}, includeDNS: true, resolvedDNS: "8.8.8.8"},
		{name: "legacy URL rejected", input: `{"dns":"https://dns.example"}`, wantField: "dns"},
		{name: "custom requires servers", input: `{"dns_mode":"custom"}`, wantField: "dns_servers"},
		{name: "servers require mode", input: `{"dns_servers":[]}`, wantField: "dns_servers"},
		{name: "explicit empty legacy conflicts with mode", input: `{"DnS":"","DNS_MODE":"system"}`, wantField: "dns"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var params AWGParams
			if err := json.Unmarshal([]byte(tt.input), &params); err != nil {
				t.Fatalf("json.Unmarshal() error = %v", err)
			}

			got, err := NormalizeOverrides(&params)
			assertValidationField(t, err, tt.wantField)
			if tt.wantField != "" {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("NormalizeOverrides() = %+v, want %+v", got, tt.want)
			}

			resolved, included := ResolveDNS(got, "8.8.8.8")
			if resolved != tt.resolvedDNS || included != tt.includeDNS {
				t.Fatalf("ResolveDNS() = (%q, %t), want (%q, %t)", resolved, included, tt.resolvedDNS, tt.includeDNS)
			}
		})
	}
}

func assertValidationField(t *testing.T, err error, wantField string) {
	t.Helper()

	if wantField == "" {
		if err != nil {
			t.Fatalf("unexpected validation error: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected validation error for %s", wantField)
	}
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("error %v does not wrap ErrInvalidParams", err)
	}

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error %v is not a ValidationError", err)
	}
	if validationErr.Field != wantField {
		t.Fatalf("validation field = %q, want %q", validationErr.Field, wantField)
	}
}
