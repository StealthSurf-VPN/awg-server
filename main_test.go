package main

import (
	"bytes"
	"errors"
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
	if got, want := calls, []string{"check-runtime"}; !sameMainStrings(got, want) {
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

	for _, secret := range []string{
		"AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=",
		"synthetic-private-key",
	} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("check-runtime output contains a secret: %q", output.String())
		}
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

	if want := []string{"prepare", "prepare-plan", "check-runtime", "new-pool", "firewall", "http"}; !sameMainStrings(order, want) {
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

func TestRunCommandUsageListsCheckRuntime(t *testing.T) {
	var output bytes.Buffer
	err := runCommand([]string{"unknown"}, mainDependencies{}, &output)
	if err == nil {
		t.Fatal("runCommand(unknown) succeeded")
	}
	if !strings.Contains(err.Error(), "usage: awg-server [version|update|check-runtime]") {
		t.Fatalf("runCommand(unknown) error = %q, want check-runtime usage", err)
	}
}

func TestTemporaryAWG31DefaultsMatchMigrationContract(t *testing.T) {
	params, err := temporaryAWG31Defaults(&config.Config{
		DNS: "1.1.1.1", Jc: 5, Jmin: 50, Jmax: 1000,
	})
	if err != nil {
		t.Fatalf("temporaryAWG31Defaults() error = %v", err)
	}

	if params.MTU != 1280 || params.RandomTrailers != "on" || params.DisableCookies != "off" {
		t.Fatalf("temporary defaults = %+v", params)
	}
	for _, tt := range []struct {
		name  string
		value string
		want  string
	}{
		{name: "persistent keepalive", value: params.PersistentKeepalive.String(), want: "25-35"},
		{name: "content padding", value: params.ContentPaddingAddition.String(), want: "10-100"},
		{name: "rekey after", value: params.RekeyAfterTime.String(), want: "100-120"},
		{name: "rekey timeout", value: params.RekeyTimeout.String(), want: "3-7"},
		{name: "reject after", value: params.RejectAfterTime.String(), want: "150-180"},
		{name: "keepalive timeout", value: params.KeepaliveTimeout.String(), want: "5-15"},
		{name: "max attempts", value: params.MaxHandshakeAttempts.String(), want: "15-20"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.value != tt.want {
				t.Fatalf("range = %q, want %q", tt.value, tt.want)
			}
		})
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

func sameMainStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}

	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}

	return true
}
