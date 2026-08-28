package awg

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const (
	testMinimumToolsPackage = "1.0.20210914-0~202608130145+ee0f0a9~ubuntu22.04.1"
	testMinimumDKMSPackage  = "1.0.0-0~202608271845+b72bb7a~ubuntu22.04.1"
	testToolsVersion        = "amneziawg-tools v3.1.20260828 - https://amnezia.org\n"
	testInstalledStatus     = "ii "
)

func TestCheckRuntimeUsesDpkgComparisonForMinimumPackages(t *testing.T) {
	tests := []struct {
		name         string
		toolsStatus  string
		dkmsStatus   string
		toolsVersion string
		dkmsVersion  string
		compareError map[string]error
		wantErr      bool
	}{
		{
			name:         "exact minimum versions",
			toolsStatus:  testInstalledStatus,
			dkmsStatus:   testInstalledStatus,
			toolsVersion: testMinimumToolsPackage,
			dkmsVersion:  testMinimumDKMSPackage,
		},
		{
			name:         "newer versions",
			toolsStatus:  testInstalledStatus,
			dkmsStatus:   testInstalledStatus,
			toolsVersion: "1.0.20210915-0~202608130145+ee0f0a9~ubuntu22.04.1",
			dkmsVersion:  "1.0.0-0~202608281845+b72bb7a~ubuntu22.04.1",
		},
		{
			name:         "held installed packages",
			toolsStatus:  "hi ",
			dkmsStatus:   "hi ",
			toolsVersion: testMinimumToolsPackage,
			dkmsVersion:  testMinimumDKMSPackage,
		},
		{
			name:         "older tools package",
			toolsStatus:  testInstalledStatus,
			dkmsStatus:   testInstalledStatus,
			toolsVersion: "1.0.20210913-0~202608130145+ee0f0a9~ubuntu22.04.1",
			dkmsVersion:  testMinimumDKMSPackage,
			compareError: map[string]error{
				"1.0.20210913-0~202608130145+ee0f0a9~ubuntu22.04.1": errors.New("exit status 1"),
			},
			wantErr: true,
		},
		{
			name:         "older dkms package",
			toolsStatus:  testInstalledStatus,
			dkmsStatus:   testInstalledStatus,
			toolsVersion: testMinimumToolsPackage,
			dkmsVersion:  "1.0.0-0~202608261845+b72bb7a~ubuntu22.04.1",
			compareError: map[string]error{
				"1.0.0-0~202608261845+b72bb7a~ubuntu22.04.1": errors.New("exit status 1"),
			},
			wantErr: true,
		},
		{
			name:         "malformed package version",
			toolsStatus:  testInstalledStatus,
			dkmsStatus:   testInstalledStatus,
			toolsVersion: "not-a-debian-version",
			dkmsVersion:  testMinimumDKMSPackage,
			compareError: map[string]error{
				"not-a-debian-version": errors.New("exit status 2"),
			},
			wantErr: true,
		},
		{
			name:         "missing package version",
			toolsStatus:  testInstalledStatus,
			dkmsStatus:   testInstalledStatus,
			toolsVersion: "",
			dkmsVersion:  testMinimumDKMSPackage,
			wantErr:      true,
		},
		{
			name:         "config files package",
			toolsStatus:  "rc ",
			dkmsStatus:   testInstalledStatus,
			toolsVersion: testMinimumToolsPackage,
			dkmsVersion:  testMinimumDKMSPackage,
			wantErr:      true,
		},
		{
			name:         "unpacked package",
			toolsStatus:  "iU ",
			dkmsStatus:   testInstalledStatus,
			toolsVersion: testMinimumToolsPackage,
			dkmsVersion:  testMinimumDKMSPackage,
			wantErr:      true,
		},
		{
			name:         "half configured package",
			toolsStatus:  "iF ",
			dkmsStatus:   testInstalledStatus,
			toolsVersion: testMinimumToolsPackage,
			dkmsVersion:  testMinimumDKMSPackage,
			wantErr:      true,
		},
		{
			name:         "package with error flag",
			toolsStatus:  "iiR",
			dkmsStatus:   testInstalledStatus,
			toolsVersion: testMinimumToolsPackage,
			dkmsVersion:  testMinimumDKMSPackage,
			wantErr:      true,
		},
		{
			name:         "malformed package status",
			toolsStatus:  "installed",
			dkmsStatus:   testInstalledStatus,
			toolsVersion: testMinimumToolsPackage,
			dkmsVersion:  testMinimumDKMSPackage,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newRuntimeHarness()
			harness.packageStatuses["amneziawg-tools"] = tt.toolsStatus
			harness.packageStatuses["amneziawg-dkms"] = tt.dkmsStatus
			harness.packageVersions["amneziawg-tools"] = tt.toolsVersion
			harness.packageVersions["amneziawg-dkms"] = tt.dkmsVersion
			harness.compareError = tt.compareError

			_, err := checkRuntime(harness.dependencies())
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkRuntime() error = %v, want error = %t", err, tt.wantErr)
			}

			if tt.wantErr {
				if harness.deleteCount() != 0 {
					t.Fatalf("failed package qualification deleted %d interfaces", harness.deleteCount())
				}
				if harness.createCount() != 0 {
					t.Fatalf("failed package qualification created %d interfaces", harness.createCount())
				}

				return
			}

			harness.requireComparison(t, tt.toolsVersion, testMinimumToolsPackage)
			harness.requireComparison(t, tt.dkmsVersion, testMinimumDKMSPackage)
		})
	}
}

func TestCheckRuntimeRequiresExactToolsVersionOutput(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantErr bool
	}{
		{name: "exact output", output: testToolsVersion},
		{name: "missing project URL", output: "amneziawg-tools v3.1.20260828\n", wantErr: true},
		{name: "wrong protocol version", output: "amneziawg-tools v3.0.20260828 - https://amnezia.org\n", wantErr: true},
		{name: "short date", output: "amneziawg-tools v3.1.2026082 - https://amnezia.org\n", wantErr: true},
		{name: "leading noise", output: "warning\namneziawg-tools v3.1.20260828 - https://amnezia.org\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newRuntimeHarness()
			harness.toolsOutput = tt.output

			_, err := checkRuntime(harness.dependencies())
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkRuntime() error = %v, want error = %t", err, tt.wantErr)
			}
			if tt.wantErr && harness.createCount() != 0 {
				t.Fatal("invalid tools output reached the interface probe")
			}
		})
	}
}

func TestCheckRuntimeRecordsModuleVersionWhileFunctionalProbeRemainsAuthoritative(t *testing.T) {
	harness := newRuntimeHarness()
	harness.moduleVersion = "unrelated-module-version\n"

	diagnostics, err := checkRuntime(harness.dependencies())
	if err != nil {
		t.Fatalf("checkRuntime() error = %v", err)
	}
	if diagnostics.ModuleVersion != "unrelated-module-version" {
		t.Fatalf("ModuleVersion = %q, want read module diagnostic", diagnostics.ModuleVersion)
	}
	if harness.createCount() != 1 || harness.deleteCount() != 1 {
		t.Fatalf("functional probe calls = create %d, delete %d, want one each", harness.createCount(), harness.deleteCount())
	}
}

func TestCheckRuntimeConfiguresAndReadsBackCompleteAWG31Profile(t *testing.T) {
	harness := newRuntimeHarness()

	if _, err := checkRuntime(harness.dependencies()); err != nil {
		t.Fatalf("checkRuntime() error = %v", err)
	}

	if got, want := harness.probeSequence(), []string{"ip link add", "awg setconf", "awg showconf", "ip link del"}; !sameStrings(got, want) {
		t.Fatalf("probe sequence = %v, want %v", got, want)
	}

	if len(harness.setconfInputs) != 1 {
		t.Fatalf("setconf input count = %d, want 1", len(harness.setconfInputs))
	}

	fields := parseRuntimeConfigForTest(t, harness.setconfInputs[0])
	wantFields := []string{
		"PrivateKey", "ListenPort", "Jc", "Jmin", "Jmax", "S1", "S2", "S3", "S4",
		"H1", "H2", "H3", "H4", "ContentPaddingAddition", "RekeyAfterTime", "RekeyTimeout",
		"RejectAfterTime", "KeepaliveTimeout", "MaxHandshakeAttempts", "RandomTrailers", "DisableCookies",
		"HeaderProtectionKey",
	}
	if len(fields) != len(wantFields) {
		t.Fatalf("probe config fields = %v, want exactly %v", fields, wantFields)
	}
	for _, field := range wantFields {
		if fields[field] == "" {
			t.Fatalf("probe config omitted %s", field)
		}
	}

	if got := harness.interfaceNames(); len(got) != 1 || len(got[0]) > 15 {
		t.Fatalf("probe interface names = %v, want one name no longer than 15 bytes", got)
	}

	for _, call := range harness.calls {
		if call.name != "awg" {
			continue
		}

		for _, secret := range []string{fields["PrivateKey"], fields["HeaderProtectionKey"]} {
			if containsString(call.args, secret) {
				t.Fatalf("awg argv contains a generated key: %v", call.args)
			}
		}
	}
}

func TestCheckRuntimeRejectsEveryReadbackMismatchWithoutLeakingKeys(t *testing.T) {
	fields := []string{
		"PrivateKey", "ListenPort", "Jc", "Jmin", "Jmax", "S1", "S2", "S3", "S4",
		"H1", "H2", "H3", "H4", "ContentPaddingAddition", "RekeyAfterTime", "RekeyTimeout",
		"RejectAfterTime", "KeepaliveTimeout", "MaxHandshakeAttempts", "RandomTrailers", "DisableCookies",
		"HeaderProtectionKey",
	}

	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			harness := newRuntimeHarness()
			harness.showconfTransform = func(config string) string {
				return replaceRuntimeConfigValue(t, config, field, "changed")
			}

			err := checkRuntimeError(harness)
			if err == nil {
				t.Fatalf("checkRuntime() accepted changed %s", field)
			}
			if harness.deleteCount() != 1 {
				t.Fatalf("readback failure deleted %d interfaces, want 1", harness.deleteCount())
			}

			configured := parseRuntimeConfigForTest(t, harness.setconfInputs[0])
			for _, secret := range []string{configured["PrivateKey"], configured["HeaderProtectionKey"], "changed"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("readback error leaks a configured value: %q", err)
				}
			}
		})
	}
}

func TestCheckRuntimeCleansUpOnlyInterfacesItCreated(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*runtimeHarness)
		want  int
	}{
		{
			name: "failure before interface creation",
			setup: func(harness *runtimeHarness) {
				harness.toolsOutput = "invalid tools output\n"
			},
			want: 0,
		},
		{
			name: "setconf failure after interface creation",
			setup: func(harness *runtimeHarness) {
				harness.setconfError = errors.New("exit status 1")
			},
			want: 1,
		},
		{
			name: "showconf failure after interface creation",
			setup: func(harness *runtimeHarness) {
				harness.showconfError = errors.New("exit status 1")
			},
			want: 1,
		},
		{
			name:  "successful probe",
			setup: func(*runtimeHarness) {},
			want:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			harness := newRuntimeHarness()
			tt.setup(harness)

			_, _ = checkRuntime(harness.dependencies())

			if got := harness.deleteCount(); got != tt.want {
				t.Fatalf("delete count = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCheckRuntimeRetriesExistingNameWithoutDeletingForeignInterface(t *testing.T) {
	harness := newRuntimeHarness()
	harness.randomNames = []string{"awgrt0000000001", "awgrt0000000002"}
	harness.createErrors["awgrt0000000001"] = runtimeCommandResult{
		output: []byte("RTNETLINK answers: File exists\n"),
		err:    errors.New("exit status 2"),
	}

	if _, err := checkRuntime(harness.dependencies()); err != nil {
		t.Fatalf("checkRuntime() error = %v", err)
	}

	if got, want := harness.interfaceNames(), []string{"awgrt0000000001", "awgrt0000000002"}; !sameStrings(got, want) {
		t.Fatalf("created interface names = %v, want %v", got, want)
	}
	if got, want := harness.deletedInterfaces(), []string{"awgrt0000000002"}; !sameStrings(got, want) {
		t.Fatalf("deleted interfaces = %v, want %v", got, want)
	}
}

func checkRuntimeError(harness *runtimeHarness) error {
	_, err := checkRuntime(harness.dependencies())

	return err
}

type runtimeCall struct {
	name  string
	args  []string
	stdin string
}

type runtimeCommandResult struct {
	output []byte
	err    error
}

type runtimeHarness struct {
	packageVersions   map[string]string
	packageStatuses   map[string]string
	compareError      map[string]error
	toolsOutput       string
	moduleVersion     string
	randomNames       []string
	randomNameIndex   int
	createErrors      map[string]runtimeCommandResult
	setconfError      error
	showconfError     error
	showconfTransform func(string) string
	setconfInputs     []string
	calls             []runtimeCall
}

func newRuntimeHarness() *runtimeHarness {
	return &runtimeHarness{
		packageVersions: map[string]string{
			"amneziawg-tools": testMinimumToolsPackage,
			"amneziawg-dkms":  testMinimumDKMSPackage,
		},
		packageStatuses: map[string]string{
			"amneziawg-tools": testInstalledStatus,
			"amneziawg-dkms":  testInstalledStatus,
		},
		toolsOutput:   testToolsVersion,
		moduleVersion: "3.1.0-test\n",
		randomNames:   []string{"awgrt0000000001"},
		createErrors:  make(map[string]runtimeCommandResult),
	}
}

func (harness *runtimeHarness) dependencies() runtimeDependencies {
	return runtimeDependencies{
		run:        harness.run,
		readFile:   harness.readFile,
		randomName: harness.randomName,
	}
}

func (harness *runtimeHarness) run(name string, args []string, stdin []byte) ([]byte, error) {
	call := runtimeCall{
		name:  name,
		args:  append([]string(nil), args...),
		stdin: string(stdin),
	}
	harness.calls = append(harness.calls, call)

	switch name {
	case "dpkg-query":
		if len(args) != 3 || args[0] != "-W" || args[1] != "-f=${db:Status-Abbrev}\\t${Version}\\n" {
			return nil, fmt.Errorf("unexpected dpkg-query arguments: %v", args)
		}

		return []byte(harness.packageStatuses[args[2]] + "\t" + harness.packageVersions[args[2]] + "\n"), nil
	case "dpkg":
		if len(args) != 4 || args[0] != "--compare-versions" || args[2] != "ge" {
			return nil, fmt.Errorf("unexpected dpkg comparison arguments: %v", args)
		}

		return nil, harness.compareError[args[1]]
	case "awg":
		if sameStrings(args, []string{"--version"}) {
			return []byte(harness.toolsOutput), nil
		}
		if len(args) == 3 && args[0] == "setconf" && args[2] == "/dev/stdin" {
			harness.setconfInputs = append(harness.setconfInputs, string(stdin))
			return nil, harness.setconfError
		}
		if len(args) == 2 && args[0] == "showconf" {
			if harness.showconfError != nil {
				return nil, harness.showconfError
			}
			if len(harness.setconfInputs) == 0 {
				return nil, errors.New("showconf before setconf")
			}

			config := harness.setconfInputs[len(harness.setconfInputs)-1]
			if harness.showconfTransform != nil {
				config = harness.showconfTransform(config)
			}

			return []byte(config), nil
		}
	case "ip":
		if len(args) == 5 && args[0] == "link" && args[1] == "add" && args[3] == "type" && args[4] == "amneziawg" {
			result := harness.createErrors[args[2]]
			return result.output, result.err
		}
		if len(args) == 3 && args[0] == "link" && args[1] == "del" {
			return nil, nil
		}
	}

	return nil, fmt.Errorf("unexpected command: %s %v", name, args)
}

func (harness *runtimeHarness) readFile(name string) ([]byte, error) {
	if name != "/sys/module/amneziawg/version" {
		return nil, fmt.Errorf("unexpected read path: %s", name)
	}

	return []byte(harness.moduleVersion), nil
}

func (harness *runtimeHarness) randomName() (string, error) {
	if harness.randomNameIndex >= len(harness.randomNames) {
		return "", errors.New("no test interface names remaining")
	}

	name := harness.randomNames[harness.randomNameIndex]
	harness.randomNameIndex++

	return name, nil
}

func (harness *runtimeHarness) requireComparison(t *testing.T, version, minimum string) {
	t.Helper()

	for _, call := range harness.calls {
		if call.name == "dpkg" && sameStrings(call.args, []string{"--compare-versions", version, "ge", minimum}) {
			return
		}
	}

	t.Fatalf("missing dpkg --compare-versions %q ge %q call", version, minimum)
}

func (harness *runtimeHarness) createCount() int {
	count := 0

	for _, call := range harness.calls {
		if call.name == "ip" && len(call.args) > 1 && call.args[0] == "link" && call.args[1] == "add" {
			count++
		}
	}

	return count
}

func (harness *runtimeHarness) deleteCount() int {
	return len(harness.deletedInterfaces())
}

func (harness *runtimeHarness) deletedInterfaces() []string {
	var names []string

	for _, call := range harness.calls {
		if call.name == "ip" && len(call.args) == 3 && call.args[0] == "link" && call.args[1] == "del" {
			names = append(names, call.args[2])
		}
	}

	return names
}

func (harness *runtimeHarness) interfaceNames() []string {
	var names []string

	for _, call := range harness.calls {
		if call.name == "ip" && len(call.args) == 5 && call.args[0] == "link" && call.args[1] == "add" {
			names = append(names, call.args[2])
		}
	}

	return names
}

func (harness *runtimeHarness) probeSequence() []string {
	var sequence []string

	for _, call := range harness.calls {
		switch {
		case call.name == "ip" && len(call.args) > 1 && call.args[0] == "link" && call.args[1] == "add":
			sequence = append(sequence, "ip link add")
		case call.name == "awg" && len(call.args) > 0 && call.args[0] == "setconf":
			sequence = append(sequence, "awg setconf")
		case call.name == "awg" && len(call.args) > 0 && call.args[0] == "showconf":
			sequence = append(sequence, "awg showconf")
		case call.name == "ip" && len(call.args) > 1 && call.args[0] == "link" && call.args[1] == "del":
			sequence = append(sequence, "ip link del")
		}
	}

	return sequence
}

func parseRuntimeConfigForTest(t *testing.T, config string) map[string]string {
	t.Helper()

	fields := make(map[string]string)

	for _, line := range strings.Split(strings.TrimSpace(config), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "[Interface]" {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid config line %q", line)
		}

		name := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		if name == "" || value == "" {
			t.Fatalf("invalid config line %q", line)
		}
		if _, exists := fields[name]; exists {
			t.Fatalf("duplicate config field %q", name)
		}

		fields[name] = value
	}

	return fields
}

func replaceRuntimeConfigValue(t *testing.T, config, field, replacement string) string {
	t.Helper()

	needle := field + " = "
	for _, line := range strings.Split(config, "\n") {
		if !strings.HasPrefix(line, needle) {
			continue
		}

		return strings.Replace(config, line, needle+replacement, 1)
	}

	t.Fatalf("config does not contain %s", field)

	return ""
}

func sameStrings(got, want []string) bool {
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}

	return false
}
