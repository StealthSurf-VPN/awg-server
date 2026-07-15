package awg

import (
	"reflect"
	"testing"
)

func TestAWGParamsKeyUsesOnlyInterfaceProfileFields(t *testing.T) {
	keepalive := 60
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
	variant.PersistentKeepalive = &keepalive
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
	keepalive := 60
	params := AWGParams{
		Port: 51820, ClientListenPort: 51821, MTU: 1380,
		DNSMode: DNSModeCustom, DNSServers: []string{"1.1.1.1"},
		PersistentKeepalive: &keepalive,
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
	keepalive := 0
	original := AWGParams{
		Port: 51820, ClientListenPort: 51821, MTU: 1380,
		DNSMode: DNSModeSystem, PersistentKeepalive: &keepalive,
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

func TestPersistentKeepaliveValue(t *testing.T) {
	disabled := 0
	custom := 60
	tests := []struct {
		name   string
		value  *int
		wanted int
	}{
		{name: "default", wanted: DefaultPersistentKeepalive},
		{name: "disabled", value: &disabled, wanted: 0},
		{name: "custom", value: &custom, wanted: 60},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := AWGParams{PersistentKeepalive: tt.value}
			if got := params.PersistentKeepaliveValue(); got != tt.wanted {
				t.Fatalf("PersistentKeepaliveValue() = %d, want %d", got, tt.wanted)
			}
		})
	}
}
