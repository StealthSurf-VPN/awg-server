package awg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureDeviceUsesSetconfStdinWithoutSecretArguments(t *testing.T) {
	argsPath, stdinPath := installFakeAWG(t)
	profile, err := NewAWG31Profile(validAWG31ProfileParams(t), syntheticHeaderProtectionKey())
	if err != nil {
		t.Fatalf("NewAWG31Profile() error = %v", err)
	}

	privateKey := [32]byte{42}
	if err := configureDevice("awg42", 51820, profile, privateKey); err != nil {
		t.Fatalf("configureDevice() error = %v", err)
	}

	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("ReadFile(args) error = %v", err)
	}
	if got, want := string(args), "setconf\nawg42\n/dev/stdin\n"; got != want {
		t.Fatalf("awg arguments = %q, want %q", got, want)
	}

	stdin, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("ReadFile(stdin) error = %v", err)
	}
	if got, want := string(stdin), profile.ServerConfig(privateKey, 51820); got != want {
		t.Fatalf("awg stdin = %q, want stripped server config", got)
	}

	privateKeyText := KeyToBase64(privateKey)
	headerKeyText := HeaderProtectionKeyToBase64(syntheticHeaderProtectionKey())
	if strings.Contains(string(args), privateKeyText) || strings.Contains(string(args), headerKeyText) {
		t.Fatalf("awg argv contains a private key: %q", args)
	}
}

func TestConfigureDeviceFailureDoesNotEchoServerConfig(t *testing.T) {
	installFakeAWG(t)
	t.Setenv("AWG_FAIL", "1")

	profile, err := NewAWG31Profile(validAWG31ProfileParams(t), syntheticHeaderProtectionKey())
	if err != nil {
		t.Fatalf("NewAWG31Profile() error = %v", err)
	}

	privateKey := [32]byte{42}
	err = configureDevice("awg42", 51820, profile, privateKey)
	if err == nil {
		t.Fatal("configureDevice() succeeded after awg failure")
	}
	if !strings.Contains(err.Error(), "awg setconf") {
		t.Fatalf("configureDevice() error = %v, want setconf context", err)
	}
	if strings.Contains(err.Error(), KeyToBase64(privateKey)) || strings.Contains(err.Error(), HeaderProtectionKeyToBase64(syntheticHeaderProtectionKey())) {
		t.Fatalf("configureDevice() error leaks a key: %q", err)
	}
}

func installFakeAWG(t *testing.T) (string, string) {
	t.Helper()

	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	stdinPath := filepath.Join(dir, "stdin")
	scriptPath := filepath.Join(dir, "awg")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$AWG_ARGS_FILE\"\ncat > \"$AWG_STDIN_FILE\"\nif [ \"$AWG_FAIL\" = \"1\" ]; then\n  printf 'synthetic awg failure\\n' >&2\n  exit 1\nfi\n"

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile(fake awg) error = %v", err)
	}

	t.Setenv("AWG_ARGS_FILE", argsPath)
	t.Setenv("AWG_STDIN_FILE", stdinPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return argsPath, stdinPath
}
