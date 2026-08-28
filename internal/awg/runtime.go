package awg

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

const (
	minimumToolsPackageVersion = "1.0.20210914-0~202608130145+ee0f0a9~ubuntu22.04.1"
	minimumDKMSPackageVersion  = "1.0.0-0~202608271845+b72bb7a~ubuntu22.04.1"
	runtimeModuleVersionPath   = "/sys/module/amneziawg/version"
	runtimeProbePort           = 51820
	runtimeInterfaceAttempts   = 8
	maxInterfaceNameLength     = 15
	runtimePackageQueryFormat  = "-f=${db:Status-Abbrev}\\t${Version}\\n"
)

var runtimeToolsVersionPattern = regexp.MustCompile(`\Aamneziawg-tools v3\.1\.[0-9]{8} - https://amnezia\.org\n?\z`)

type RuntimeDiagnostics struct {
	ToolsPackageVersion string
	DKMSPackageVersion  string
	ToolsVersion        string
	ModuleVersion       string
}

type runtimeDependencies struct {
	run        func(string, []string, []byte) ([]byte, error)
	readFile   func(string) ([]byte, error)
	randomName func() (string, error)
}

func CheckRuntime() (RuntimeDiagnostics, error) {
	return checkRuntime(runtimeDependencies{
		run:        runRuntimeCommand,
		readFile:   os.ReadFile,
		randomName: generateRuntimeInterfaceName,
	})
}

func checkRuntime(dependencies runtimeDependencies) (RuntimeDiagnostics, error) {
	toolsPackageVersion, err := checkRuntimePackage(
		dependencies,
		"amneziawg-tools",
		minimumToolsPackageVersion,
	)
	if err != nil {
		return RuntimeDiagnostics{}, err
	}

	dkmsPackageVersion, err := checkRuntimePackage(
		dependencies, "amneziawg-dkms", minimumDKMSPackageVersion)
	if err != nil {
		return RuntimeDiagnostics{}, err
	}

	toolsOutput, err := dependencies.run("awg", []string{"--version"}, nil)
	if err != nil {
		return RuntimeDiagnostics{}, fmt.Errorf("read awg tools version: %w", err)
	}
	if !runtimeToolsVersionPattern.Match(toolsOutput) {
		return RuntimeDiagnostics{}, errors.New("unexpected awg tools version output")
	}

	diagnostics := RuntimeDiagnostics{
		ToolsPackageVersion: toolsPackageVersion,
		DKMSPackageVersion:  dkmsPackageVersion,
		ToolsVersion:        strings.TrimSuffix(string(toolsOutput), "\n"),
		ModuleVersion:       "unavailable",
	}

	if moduleVersion, readErr := dependencies.readFile(runtimeModuleVersionPath); readErr == nil {
		diagnostics.ModuleVersion = strings.TrimSuffix(string(moduleVersion), "\n")
	}

	if err := probeRuntime(dependencies); err != nil {
		return RuntimeDiagnostics{}, err
	}

	return diagnostics, nil
}

func checkRuntimePackage(dependencies runtimeDependencies, packageName, minimumVersion string) (string, error) {
	output, err := dependencies.run("dpkg-query", []string{"-W", runtimePackageQueryFormat, packageName}, nil)
	if err != nil {
		return "", fmt.Errorf("query %s package version: %w", packageName, err)
	}

	version, err := parseInstalledPackageVersion(output)
	if err != nil {
		return "", fmt.Errorf("query %s package status: %w", packageName, err)
	}

	if _, err := dependencies.run("dpkg", []string{"--compare-versions", version, "ge", minimumVersion}, nil); err != nil {
		return "", fmt.Errorf("verify %s package version: %w", packageName, err)
	}

	return version, nil
}

func parseInstalledPackageVersion(output []byte) (string, error) {
	result := string(output)
	if !strings.HasSuffix(result, "\n") {
		return "", errors.New("malformed output")
	}

	result = strings.TrimSuffix(result, "\n")
	if strings.Contains(result, "\n") {
		return "", errors.New("malformed output")
	}

	fields := strings.Split(result, "\t")
	if len(fields) != 2 || !installedOKPackageStatus(fields[0]) || fields[1] == "" {
		return "", errors.New("package is not installed and ok")
	}

	return fields[1], nil
}

func installedOKPackageStatus(status string) bool {
	if len(status) != 3 || status[1] != 'i' || status[2] != ' ' {
		return false
	}

	return strings.ContainsRune("uihrp", rune(status[0]))
}

func probeRuntime(dependencies runtimeDependencies) error {
	privateKey, err := GeneratePrivateKey()
	if err != nil {
		return fmt.Errorf("generate runtime probe private key: %w", err)
	}

	headerProtectionKey, err := GenerateHeaderProtectionKey()
	if err != nil {
		return fmt.Errorf("generate runtime probe header protection key: %w", err)
	}

	profile, err := newRuntimeProbeProfile(headerProtectionKey)
	if err != nil {
		return err
	}

	for attempt := 0; attempt < runtimeInterfaceAttempts; attempt++ {
		ifName, err := dependencies.randomName()
		if err != nil {
			return fmt.Errorf("generate runtime probe interface name: %w", err)
		}
		if err := validateRuntimeInterfaceName(ifName); err != nil {
			return err
		}

		output, err := dependencies.run("ip", []string{"link", "add", ifName, "type", "amneziawg"}, nil)
		if err != nil {
			if runtimeInterfaceExists(output) {
				continue
			}

			return fmt.Errorf("create runtime probe interface: %w", err)
		}

		return probeCreatedRuntimeInterface(dependencies, ifName, profile, privateKey)
	}

	return errors.New("create runtime probe interface: exhausted collision-safe names")
}

func probeCreatedRuntimeInterface(dependencies runtimeDependencies, ifName string, profile Profile, privateKey [32]byte) (err error) {
	defer func() {
		if _, cleanupErr := dependencies.run("ip", []string{"link", "del", ifName}, nil); cleanupErr != nil {
			cleanupErr = fmt.Errorf("delete runtime probe interface: %w", cleanupErr)
			if err == nil {
				err = cleanupErr
			} else {
				err = errors.Join(err, cleanupErr)
			}
		}
	}()

	serverConfig := profile.ServerConfig(privateKey, runtimeProbePort)
	if _, err := dependencies.run("awg", []string{"setconf", ifName, "/dev/stdin"}, []byte(serverConfig)); err != nil {
		return fmt.Errorf("configure runtime probe interface: %w", err)
	}

	readback, err := dependencies.run("awg", []string{"showconf", ifName}, nil)
	if err != nil {
		return fmt.Errorf("read runtime probe interface configuration: %w", err)
	}

	if err := compareRuntimeConfig(serverConfig, string(readback)); err != nil {
		return err
	}

	return nil
}

func newRuntimeProbeProfile(headerProtectionKey HeaderProtectionKey) (Profile, error) {
	persistentKeepalive, err := config.ParseUint16Range("25-35")
	if err != nil {
		return Profile{}, fmt.Errorf("build runtime probe persistent keepalive: %w", err)
	}
	contentPaddingAddition, err := config.ParseUint16Range("10-100")
	if err != nil {
		return Profile{}, fmt.Errorf("build runtime probe content padding: %w", err)
	}
	rekeyAfterTime, err := config.ParseUint16Range("100-120")
	if err != nil {
		return Profile{}, fmt.Errorf("build runtime probe rekey after time: %w", err)
	}
	rekeyTimeout, err := config.ParseUint16Range("3-7")
	if err != nil {
		return Profile{}, fmt.Errorf("build runtime probe rekey timeout: %w", err)
	}
	rejectAfterTime, err := config.ParseUint16Range("150-180")
	if err != nil {
		return Profile{}, fmt.Errorf("build runtime probe reject after time: %w", err)
	}
	keepaliveTimeout, err := config.ParseUint16Range("5-15")
	if err != nil {
		return Profile{}, fmt.Errorf("build runtime probe keepalive timeout: %w", err)
	}
	maxHandshakeAttempts, err := config.ParseUint16Range("15-20")
	if err != nil {
		return Profile{}, fmt.Errorf("build runtime probe max handshake attempts: %w", err)
	}

	return NewAWG31Profile(AWGParams{
		Jc: 5, Jmin: 50, Jmax: 1000,
		S1: 15, S2: 72, S3: 15, S4: 12,
		H1: "100001", H2: "1000001", H3: "10000001", H4: "100000001",
		PersistentKeepalive:    &persistentKeepalive,
		ContentPaddingAddition: &contentPaddingAddition,
		RekeyAfterTime:         &rekeyAfterTime,
		RekeyTimeout:           &rekeyTimeout,
		RejectAfterTime:        &rejectAfterTime,
		KeepaliveTimeout:       &keepaliveTimeout,
		MaxHandshakeAttempts:   &maxHandshakeAttempts,
		RandomTrailers:         "on",
		DisableCookies:         "off",
	}, headerProtectionKey)
}

func compareRuntimeConfig(expectedConfig, actualConfig string) error {
	expected, err := parseRuntimeConfig(expectedConfig)
	if err != nil {
		return fmt.Errorf("parse expected runtime probe configuration: %w", err)
	}

	actual, err := parseRuntimeConfig(actualConfig)
	if err != nil {
		return fmt.Errorf("parse runtime probe readback: %w", err)
	}

	for field, expectedValue := range expected {
		if actual[field] != expectedValue {
			return fmt.Errorf("runtime probe readback mismatch for %s", field)
		}
	}

	return nil
}

func parseRuntimeConfig(value string) (map[string]string, error) {
	fields := make(map[string]string)

	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "[Interface]" {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			return nil, errors.New("invalid configuration line")
		}

		field := strings.TrimSpace(parts[0])
		fieldValue := strings.TrimSpace(parts[1])
		if field == "" || fieldValue == "" {
			return nil, errors.New("invalid configuration field")
		}
		if _, exists := fields[field]; exists {
			return nil, errors.New("duplicate configuration field")
		}

		fields[field] = fieldValue
	}

	return fields, nil
}

func generateRuntimeInterfaceName() (string, error) {
	var randomBytes [5]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return "", err
	}

	return "awgrt" + hex.EncodeToString(randomBytes[:]), nil
}

func validateRuntimeInterfaceName(ifName string) error {
	if ifName == "" || len(ifName) > maxInterfaceNameLength {
		return errors.New("invalid runtime probe interface name")
	}

	for _, character := range ifName {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return errors.New("invalid runtime probe interface name")
		}
	}

	return nil
}

func runtimeInterfaceExists(output []byte) bool {
	return strings.Contains(strings.ToLower(string(output)), "file exists")
}

func runRuntimeCommand(name string, args []string, stdin []byte) ([]byte, error) {
	command := exec.Command(name, args...)
	if len(stdin) > 0 {
		command.Stdin = bytes.NewReader(stdin)
	}

	return command.CombinedOutput()
}
