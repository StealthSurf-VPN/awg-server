package awg

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

func TestAWGParamsKeyUsesOnlyInterfaceProfileFields(t *testing.T) {
	base := AWGParams{
		H1: "1-2", H2: "3-4", H3: "5-6", H4: "7-8",
		S1: 10, S2: 20, S3: 30, S4: 31,
	}
	variant := base
	variant.Port = 51820
	variant.ClientListenPort = 51821
	variant.MTU = 1380
	variant.DNS = "9.9.9.9"
	variant.DNSMode = DNSModeCustom
	variant.DNSServers = []string{"1.1.1.1"}
	variant.PersistentKeepalive = rangePointer(t, "60")
	variant.Jc = 5
	variant.Jmin = 50
	variant.Jmax = 1000
	variant.I1 = "<t>"
	variant.I2 = "<r 10>"
	variant.I3 = "<rc 10>"
	variant.I4 = "<rd 10>"
	variant.I5 = "<b 0xff>"

	want := "h1=1-2,h2=3-4,h3=5-6,h4=7-8,s1=10,s2=20,s3=30,s4=31"
	if got := base.Key(); got != want {
		t.Fatalf("Key() = %q, want %q", got, want)
	}
	if got := variant.Key(); got != want {
		t.Fatalf("Key() changed for client-only or non-grouping fields: %q", got)
	}

	changed := base
	changed.S4++
	if changed.Key() == base.Key() {
		t.Fatal("Key() did not change for an interface profile field")
	}
}

func TestAWGParamsWireRepresentations(t *testing.T) {
	params := AWGParams{
		Port: 51820, ClientListenPort: 51821, MTU: 1380,
		DNSMode: DNSModeCustom, DNSServers: []string{"1.1.1.1"},
		PersistentKeepalive: rangePointer(t, "60"),
		Jc:                  5, Jmin: 50, Jmax: 1000,
		S1: 15, S2: 72, S3: 0, S4: 0,
		H1: "1-2", H2: "3-4", H3: "5-6", H4: "7-8",
		I1: "<t>", I2: "<r 10>",
	}

	wantArgs := []string{
		"jc", "5", "jmin", "50", "jmax", "1000",
		"s1", "15", "s2", "72", "s3", "0", "s4", "0",
		"h1", "1-2", "h2", "3-4", "h3", "5-6", "h4", "7-8",
	}
	if got := params.CLIArgs(); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("CLIArgs() = %v, want %v", got, wantArgs)
	}

	wantLines := "\nJc = 5\nJmin = 50\nJmax = 1000" +
		"\nS1 = 15\nS2 = 72\nS3 = 0\nS4 = 0" +
		"\nH1 = 1-2\nH2 = 3-4\nH3 = 5-6\nH4 = 7-8" +
		"\nI1 = <t>\nI2 = <r 10>"
	if got := params.ConfigLines(); got != wantLines {
		t.Fatalf("ConfigLines() = %q, want %q", got, wantLines)
	}
}

func TestAWGParamsV31WireRepresentations(t *testing.T) {
	params := AWGParams{
		ContentPaddingAddition: rangePointer(t, "10-100"),
		RekeyAfterTime:         rangePointer(t, "100-120"),
		RekeyTimeout:           rangePointer(t, "3-7"),
		RejectAfterTime:        rangePointer(t, "150-180"),
		KeepaliveTimeout:       rangePointer(t, "5-15"),
		MaxHandshakeAttempts:   rangePointer(t, "15-20"),
		RandomTrailers:         "on",
		DisableCookies:         "off",
	}

	wantArgs := []string{
		"s3", "0", "s4", "0",
		"content-padding-addition", "10-100",
		"rekey-after-time", "100-120",
		"rekey-timeout", "3-7",
		"reject-after-time", "150-180",
		"keepalive-timeout", "5-15",
		"max-handshake-attempts", "15-20",
		"random-trailers", "on",
		"disable-cookies", "off",
	}
	if got := params.CLIArgs(); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("CLIArgs() = %v, want %v", got, wantArgs)
	}

	wantLines := "\nS3 = 0\nS4 = 0" +
		"\nContentPaddingAddition = 10-100" +
		"\nRekeyAfterTime = 100-120" +
		"\nRekeyTimeout = 3-7" +
		"\nRejectAfterTime = 150-180" +
		"\nKeepaliveTimeout = 5-15" +
		"\nMaxHandshakeAttempts = 15-20" +
		"\nRandomTrailers = on" +
		"\nDisableCookies = off"
	if got := params.ConfigLines(); got != wantLines {
		t.Fatalf("ConfigLines() = %q, want %q", got, wantLines)
	}

	if _, found := reflect.TypeOf(AWGParams{}).FieldByName("HeaderProtectionKey"); found {
		t.Fatal("AWGParams must not contain HeaderProtectionKey")
	}
}

func TestGenerateParamsInvariants(t *testing.T) {
	tiers := []struct {
		name  string
		min   uint32
		max   uint32
		value func(*GeneratedParams) string
	}{
		{name: "h1", min: 100_000, max: 800_000, value: func(p *GeneratedParams) string { return p.H1 }},
		{name: "h2", min: 1_000_000, max: 8_000_000, value: func(p *GeneratedParams) string { return p.H2 }},
		{name: "h3", min: 10_000_000, max: 80_000_000, value: func(p *GeneratedParams) string { return p.H3 }},
		{name: "h4", min: 100_000_000, max: 800_000_000, value: func(p *GeneratedParams) string { return p.H4 }},
	}

	for iteration := 0; iteration < 100; iteration++ {
		generated, err := GenerateParams()
		if err != nil {
			t.Fatalf("GenerateParams() error = %v", err)
		}
		if generated.S1 < 15 || generated.S1 > 150 {
			t.Fatalf("S1 = %d, want 15-150", generated.S1)
		}
		if generated.S2 < 15 || generated.S2 > 150 {
			t.Fatalf("S2 = %d, want 15-150", generated.S2)
		}
		if generated.S1+56 == generated.S2 {
			t.Fatalf("S2 = S1 + 56 for %+v", generated)
		}

		for _, tier := range tiers {
			parsed, err := parseHeaderRange(tier.name, tier.value(generated))
			if err != nil {
				t.Fatalf("%s range %q is invalid: %v", tier.name, tier.value(generated), err)
			}
			if parsed.start < tier.min || parsed.end >= tier.max {
				t.Fatalf("%s range = %d-%d, want within %d-%d", tier.name, parsed.start, parsed.end, tier.min, tier.max-1)
			}
		}

		profile := AWGParams{
			H1: generated.H1, H2: generated.H2, H3: generated.H3, H4: generated.H4,
			S1: generated.S1, S2: generated.S2,
		}
		if err := ValidateProfile(profile); err != nil {
			t.Fatalf("generated profile is invalid: %v", err)
		}
	}
}

func TestApplyGeneratedParamsPreservesOtherOverrides(t *testing.T) {
	original := AWGParams{
		Port: 51820, ClientListenPort: 51821, MTU: 1380,
		DNSMode: DNSModeSystem, PersistentKeepalive: rangePointer(t, "0"),
		Jc: 5, Jmin: 50, Jmax: 1000, S3: 20, S4: 10,
		H1: "old-1", H2: "old-2", H3: "old-3", H4: "old-4",
		S1: 10, S2: 20, I1: "<t>",
	}
	generated := GeneratedParams{
		H1: "1-2", H2: "3-4", H3: "5-6", H4: "7-8",
		S1: 30, S2: 40,
	}

	got := ApplyGeneratedParams(&original, generated)
	want := original
	want.H1 = generated.H1
	want.H2 = generated.H2
	want.H3 = generated.H3
	want.H4 = generated.H4
	want.S1 = generated.S1
	want.S2 = generated.S2

	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("ApplyGeneratedParams() = %+v, want %+v", *got, want)
	}
	if original.H1 != "old-1" || original.S1 != 10 {
		t.Fatalf("ApplyGeneratedParams() mutated input: %+v", original)
	}
}

func TestGenerateParamsV31Invariants(t *testing.T) {
	tiers := []struct {
		name  string
		min   uint32
		max   uint32
		value func(*GeneratedParamsV31) string
	}{
		{name: "h1", min: 100_000, max: 800_000, value: func(p *GeneratedParamsV31) string { return p.H1 }},
		{name: "h2", min: 1_000_000, max: 8_000_000, value: func(p *GeneratedParamsV31) string { return p.H2 }},
		{name: "h3", min: 10_000_000, max: 80_000_000, value: func(p *GeneratedParamsV31) string { return p.H3 }},
		{name: "h4", min: 100_000_000, max: 800_000_000, value: func(p *GeneratedParamsV31) string { return p.H4 }},
	}

	for iteration := 0; iteration < 100; iteration++ {
		generated, err := GenerateParamsV31()
		if err != nil {
			t.Fatalf("GenerateParamsV31() error = %v", err)
		}
		if generated.S1 < 15 || generated.S1 > 150 {
			t.Fatalf("S1 = %d, want 15-150", generated.S1)
		}
		if generated.S2 < 15 || generated.S2 > 150 {
			t.Fatalf("S2 = %d, want 15-150", generated.S2)
		}
		if generated.S3 < 15 || generated.S3 > 63 {
			t.Fatalf("S3 = %d, want 15-63", generated.S3)
		}
		if generated.S4 != 12 {
			t.Fatalf("S4 = %d, want 12", generated.S4)
		}
		if generated.S1+56 == generated.S2 {
			t.Fatalf("S2 = S1 + 56 for %+v", generated)
		}

		seen := make(map[uint32]bool, len(tiers))
		for _, tier := range tiers {
			value := tier.value(generated)
			parsed, err := strconv.ParseUint(value, 10, 32)
			if err != nil {
				t.Fatalf("%s = %q, want fixed unsigned decimal: %v", tier.name, value, err)
			}
			if parsed < uint64(tier.min) || parsed >= uint64(tier.max) {
				t.Fatalf("%s = %d, want within %d-%d", tier.name, parsed, tier.min, tier.max-1)
			}
			if seen[uint32(parsed)] {
				t.Fatalf("duplicate fixed H value %d", parsed)
			}
			seen[uint32(parsed)] = true
		}

		profile := AWGParams{
			H1: generated.H1, H2: generated.H2, H3: generated.H3, H4: generated.H4,
			S1: generated.S1, S2: generated.S2, S3: generated.S3, S4: generated.S4,
		}
		if err := ValidateProfileForVersion(ProtocolVersion31, profile); err != nil {
			t.Fatalf("generated 3.1 profile is invalid: %v", err)
		}
	}
}

func TestApplyGeneratedParamsV31PreservesOtherOverrides(t *testing.T) {
	original := AWGParams{
		Port: 51820, ClientListenPort: 51821, MTU: 1380,
		DNSMode: DNSModeSystem, PersistentKeepalive: rangePointer(t, "off"),
		Jc: 5, Jmin: 50, Jmax: 1000,
		H1: "old-1", H2: "old-2", H3: "old-3", H4: "old-4",
		S1: 10, S2: 20, S3: 20, S4: 10, I1: "<t>",
		ContentPaddingAddition: rangePointer(t, "10-100"),
		RekeyAfterTime:         rangePointer(t, "100-120"),
		RekeyTimeout:           rangePointer(t, "3-7"),
		RejectAfterTime:        rangePointer(t, "150-180"),
		KeepaliveTimeout:       rangePointer(t, "5-15"),
		MaxHandshakeAttempts:   rangePointer(t, "15-20"),
		RandomTrailers:         "on",
		DisableCookies:         "off",
	}
	generated := GeneratedParamsV31{
		H1: "1", H2: "2", H3: "3", H4: "4",
		S1: 30, S2: 40, S3: 20, S4: 12,
	}

	got := ApplyGeneratedParamsV31(&original, generated)
	want := original
	want.H1 = generated.H1
	want.H2 = generated.H2
	want.H3 = generated.H3
	want.H4 = generated.H4
	want.S1 = generated.S1
	want.S2 = generated.S2
	want.S3 = generated.S3
	want.S4 = generated.S4

	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("ApplyGeneratedParamsV31() = %+v, want %+v", *got, want)
	}
	if original.H1 != "old-1" || original.S1 != 10 || original.S3 != 20 || original.S4 != 10 {
		t.Fatalf("ApplyGeneratedParamsV31() mutated input: %+v", original)
	}
	if got.PersistentKeepalive == original.PersistentKeepalive || got.ContentPaddingAddition == original.ContentPaddingAddition {
		t.Fatal("ApplyGeneratedParamsV31() did not deep-clone range pointers")
	}
}

func TestPersistentKeepaliveConfigValue(t *testing.T) {
	tests := []struct {
		name    string
		version ProtocolVersion
		value   *config.Uint16Range
		wanted  string
	}{
		{name: "legacy default", version: ProtocolVersion2, wanted: "25"},
		{name: "inherited 3.1 default", version: ProtocolVersion31, value: rangePointer(t, "25-35"), wanted: "25-35"},
		{name: "explicit scalar zero", version: ProtocolVersion31, value: rangePointer(t, "0"), wanted: "0"},
		{name: "explicit off", version: ProtocolVersion31, value: rangePointer(t, "off"), wanted: "off"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := AWGParams{PersistentKeepalive: tt.value}
			if got := params.PersistentKeepaliveConfigValue(tt.version); got != tt.wanted {
				t.Fatalf("PersistentKeepaliveConfigValue() = %q, want %q", got, tt.wanted)
			}
		})
	}
}

func rangePointer(t *testing.T, value string) *config.Uint16Range {
	t.Helper()

	parsed, err := config.ParseUint16Range(value)
	if err != nil {
		t.Fatalf("ParseUint16Range(%q) error = %v", value, err)
	}

	return &parsed
}
