package main

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stealthsurf-vpn/awg-server/internal/awg"
	"github.com/stealthsurf-vpn/awg-server/internal/clients"
	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

func TestRunCommandCheckRuntimeBypassesStartupPreparation(t *testing.T) {
	calls := []string{}
	dependencies := mainDependencies{
		checkRuntime: func() (awg.RuntimeDiagnostics, error) {
			calls = append(calls, "check-runtime")

			return runtimeDiagnosticsForTest(), nil
		},
		prepareStartup: func() (*startupState, error) {
			t.Fatal("check-runtime loaded API configuration or storage")

			return nil, nil
		},
		prepareRestorePlan: func(*startupState) (*clients.RestorePlan, error) {
			t.Fatal("check-runtime prepared a restore plan")

			return nil, nil
		},
		startQualified: func(*startupState, *clients.RestorePlan) error {
			t.Fatal("check-runtime started the qualified server")

			return nil
		},
	}

	var output bytes.Buffer
	if err := runCommand([]string{"check-runtime"}, dependencies, &output); err != nil {
		t.Fatalf("runCommand(check-runtime) error = %v", err)
	}
	if got, want := calls, []string{"check-runtime"}; !slices.Equal(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}

	wantOutput := "AWG 3.1 runtime qualified\n" +
		"amneziawg-tools package: 1.0.20210914-0~202608130145+ee0f0a9~ubuntu22.04.1\n" +
		"amneziawg-dkms package: 1.0.0-0~202608271845+b72bb7a~ubuntu22.04.1\n" +
		"tools version: amneziawg-tools v3.1.20260828 - https://amnezia.org\n" +
		"module version: 3.1.0-test\n"
	if got := output.String(); got != wantOutput {
		t.Fatalf("check-runtime output = %q, want %q", got, wantOutput)
	}
}

func TestRunApplicationQualifiesRuntimeBeforePoolFirewallAndHTTP(t *testing.T) {
	order := []string{}
	dependencies := mainDependencies{
		prepareStartup: func() (*startupState, error) {
			order = append(order, "prepare")

			return &startupState{}, nil
		},
		prepareRestorePlan: func(*startupState) (*clients.RestorePlan, error) {
			order = append(order, "prepare-plan")

			return &clients.RestorePlan{}, nil
		},
		checkRuntime: func() (awg.RuntimeDiagnostics, error) {
			order = append(order, "check-runtime")

			return runtimeDiagnosticsForTest(), nil
		},
		startQualified: func(*startupState, *clients.RestorePlan) error {
			order = append(order, "new-pool", "firewall", "http")

			return nil
		},
	}

	if err := runApplication(dependencies); err != nil {
		t.Fatalf("runApplication() error = %v", err)
	}

	if want := []string{"prepare", "prepare-plan", "check-runtime", "new-pool", "firewall", "http"}; !slices.Equal(order, want) {
		t.Fatalf("startup order = %v, want %v", order, want)
	}
}

func TestRunApplicationStopsBeforePoolFirewallAndHTTPWhenRuntimeFails(t *testing.T) {
	mutations := 0
	dependencies := mainDependencies{
		prepareStartup: func() (*startupState, error) {
			return &startupState{}, nil
		},
		prepareRestorePlan: func(*startupState) (*clients.RestorePlan, error) {
			return &clients.RestorePlan{}, nil
		},
		checkRuntime: func() (awg.RuntimeDiagnostics, error) {
			return awg.RuntimeDiagnostics{}, errors.New("runtime probe failed")
		},
		startQualified: func(*startupState, *clients.RestorePlan) error {
			mutations++

			return nil
		},
	}

	err := runApplication(dependencies)
	if err == nil || !strings.Contains(err.Error(), "runtime probe failed") {
		t.Fatalf("runApplication() error = %v, want runtime qualification failure", err)
	}
	if mutations != 0 {
		t.Fatalf("runtime failure reached %d pool/firewall/API mutations", mutations)
	}
}

func TestRunApplicationStopsBeforeRuntimeWhenRestorePlanFails(t *testing.T) {
	mutations := 0
	dependencies := mainDependencies{
		prepareStartup: func() (*startupState, error) {
			return &startupState{}, nil
		},
		prepareRestorePlan: func(*startupState) (*clients.RestorePlan, error) {
			return nil, errors.New("persisted profile is invalid")
		},
		checkRuntime: func() (awg.RuntimeDiagnostics, error) {
			mutations++

			return awg.RuntimeDiagnostics{}, nil
		},
		startQualified: func(*startupState, *clients.RestorePlan) error {
			mutations++

			return nil
		},
	}

	err := runApplication(dependencies)
	if err == nil || !strings.Contains(err.Error(), "persisted profile is invalid") {
		t.Fatalf("runApplication() error = %v, want restore-plan failure", err)
	}
	if mutations != 0 {
		t.Fatalf("restore-plan failure reached %d runtime/pool/API mutations", mutations)
	}
}

func TestManagerDefaultsFromConfigUsesValidatedAWG31Settings(t *testing.T) {
	parseRange := func(value string) config.Uint16Range {
		t.Helper()

		parsed, err := config.ParseUint16Range(value)
		if err != nil {
			t.Fatalf("ParseUint16Range(%q) error = %v", value, err)
		}

		return parsed
	}

	cfg := &config.Config{
		MTU:                         1420,
		DNS:                         "1.1.1.1",
		Jc:                          5,
		Jmin:                        50,
		Jmax:                        1000,
		S3:                          0,
		S4:                          0,
		I1:                          "b5",
		DefaultProtocolVersion:      "2.0",
		AWG31MTU:                    1280,
		AWG31PersistentKeepalive:    parseRange("25-35"),
		AWG31ContentPaddingAddition: parseRange("10-100"),
		AWG31RekeyAfterTime:         parseRange("100-120"),
		AWG31RekeyTimeout:           parseRange("3-7"),
		AWG31RejectAfterTime:        parseRange("150-180"),
		AWG31KeepaliveTimeout:       parseRange("5-15"),
		AWG31MaxHandshakeAttempts:   parseRange("15-20"),
		AWG31RandomTrailers:         "on",
		AWG31DisableCookies:         "off",
	}
	data := &clients.StorageData{
		GeneratedParams: &awg.GeneratedParams{
			H1: "100000-200000", H2: "1000000-2000000",
			H3: "10000000-20000000", H4: "100000000-200000000",
			S1: 15, S2: 72,
		},
	}

	defaults, err := managerDefaultsFromConfig(cfg, data)
	if err != nil {
		t.Fatalf("managerDefaultsFromConfig() error = %v", err)
	}

	if defaults.DefaultVersion != awg.ProtocolVersion2 {
		t.Fatalf("DefaultVersion = %q, want 2.0", defaults.DefaultVersion)
	}
	if defaults.LegacyParams.H1 != data.GeneratedParams.H1 || defaults.LegacyParams.S2 != data.GeneratedParams.S2 {
		t.Fatalf("legacy defaults did not use persisted generated params: %+v", defaults.LegacyParams)
	}
	if defaults.AWG31Params.MTU != cfg.AWG31MTU || defaults.AWG31Params.RandomTrailers != "on" || defaults.AWG31Params.DisableCookies != "off" {
		t.Fatalf("AWG 3.1 defaults = %+v", defaults.AWG31Params)
	}

	for _, tt := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "persistent keepalive", value: defaults.AWG31Params.PersistentKeepalive.String(), want: "25-35"},
		{name: "content padding", value: defaults.AWG31Params.ContentPaddingAddition.String(), want: "10-100"},
		{name: "rekey after", value: defaults.AWG31Params.RekeyAfterTime.String(), want: "100-120"},
		{name: "rekey timeout", value: defaults.AWG31Params.RekeyTimeout.String(), want: "3-7"},
		{name: "reject after", value: defaults.AWG31Params.RejectAfterTime.String(), want: "150-180"},
		{name: "keepalive timeout", value: defaults.AWG31Params.KeepaliveTimeout.String(), want: "5-15"},
		{name: "max attempts", value: defaults.AWG31Params.MaxHandshakeAttempts.String(), want: "15-20"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value != tt.want {
				t.Fatalf("range = %q, want %q", tt.value, tt.want)
			}
		})
	}
}

func TestManagerDefaultsFromConfigRejectsNonCanonicalProtocolVersion(t *testing.T) {
	_, err := managerDefaultsFromConfig(&config.Config{DefaultProtocolVersion: "2"}, &clients.StorageData{
		GeneratedParams: &awg.GeneratedParams{},
	})
	if err == nil || !strings.Contains(err.Error(), "protocol version") {
		t.Fatalf("managerDefaultsFromConfig() error = %v, want protocol version error", err)
	}
}

func runtimeDiagnosticsForTest() awg.RuntimeDiagnostics {
	return awg.RuntimeDiagnostics{
		ToolsPackageVersion: "1.0.20210914-0~202608130145+ee0f0a9~ubuntu22.04.1",
		DKMSPackageVersion:  "1.0.0-0~202608271845+b72bb7a~ubuntu22.04.1",
		ToolsVersion:        "amneziawg-tools v3.1.20260828 - https://amnezia.org",
		ModuleVersion:       "3.1.0-test",
	}
}
