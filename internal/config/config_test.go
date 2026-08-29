package config

import (
	"strings"
	"testing"
)

func TestLoadAWG31Defaults(t *testing.T) {
	setRequiredConfigEnvironment(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.DefaultProtocolVersion != "3.1" {
		t.Fatalf("DefaultProtocolVersion = %q, want 3.1", cfg.DefaultProtocolVersion)
	}
	if cfg.AWG31MTU != 1280 {
		t.Fatalf("AWG31MTU = %d, want 1280", cfg.AWG31MTU)
	}

	for _, tt := range []struct {
		name  string
		value Uint16Range
		want  string
	}{
		{name: "persistent keepalive", value: cfg.AWG31PersistentKeepalive, want: "25-35"},
		{name: "content padding addition", value: cfg.AWG31ContentPaddingAddition, want: "10-100"},
		{name: "rekey after time", value: cfg.AWG31RekeyAfterTime, want: "100-120"},
		{name: "rekey timeout", value: cfg.AWG31RekeyTimeout, want: "3-7"},
		{name: "reject after time", value: cfg.AWG31RejectAfterTime, want: "150-180"},
		{name: "keepalive timeout", value: cfg.AWG31KeepaliveTimeout, want: "5-15"},
		{name: "max handshake attempts", value: cfg.AWG31MaxHandshakeAttempts, want: "15-20"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.value.String(); got != tt.want {
				t.Fatalf("range = %q, want %q", got, tt.want)
			}
		})
	}

	if cfg.AWG31RandomTrailers != "on" {
		t.Fatalf("AWG31RandomTrailers = %q, want on", cfg.AWG31RandomTrailers)
	}
	if cfg.AWG31DisableCookies != "off" {
		t.Fatalf("AWG31DisableCookies = %q, want off", cfg.AWG31DisableCookies)
	}
}

func TestLoadAWG31EnvironmentOverridesAndNormalizesProtocolAlias(t *testing.T) {
	setRequiredConfigEnvironment(t)
	t.Setenv("AWG_DEFAULT_PROTOCOL_VERSION", "2")
	t.Setenv("AWG31_MTU", "1420")
	t.Setenv("AWG31_PERSISTENT_KEEPALIVE", "40")
	t.Setenv("AWG31_CONTENT_PADDING_ADDITION", "1-2")
	t.Setenv("AWG31_REKEY_AFTER_TIME", "3")
	t.Setenv("AWG31_REKEY_TIMEOUT", "4-5")
	t.Setenv("AWG31_REJECT_AFTER_TIME", "6")
	t.Setenv("AWG31_KEEPALIVE_TIMEOUT", "7-8")
	t.Setenv("AWG31_MAX_HANDSHAKE_ATTEMPTS", "off")
	t.Setenv("AWG31_RANDOM_TRAILERS", "off")
	t.Setenv("AWG31_DISABLE_COOKIES", "on")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.DefaultProtocolVersion != "2.0" {
		t.Fatalf("DefaultProtocolVersion = %q, want 2.0", cfg.DefaultProtocolVersion)
	}
	if cfg.AWG31MTU != 1420 {
		t.Fatalf("AWG31MTU = %d, want 1420", cfg.AWG31MTU)
	}

	for _, tt := range []struct {
		name  string
		value Uint16Range
		want  string
	}{
		{name: "persistent keepalive", value: cfg.AWG31PersistentKeepalive, want: "40"},
		{name: "content padding addition", value: cfg.AWG31ContentPaddingAddition, want: "1-2"},
		{name: "rekey after time", value: cfg.AWG31RekeyAfterTime, want: "3"},
		{name: "rekey timeout", value: cfg.AWG31RekeyTimeout, want: "4-5"},
		{name: "reject after time", value: cfg.AWG31RejectAfterTime, want: "6"},
		{name: "keepalive timeout", value: cfg.AWG31KeepaliveTimeout, want: "7-8"},
		{name: "max handshake attempts", value: cfg.AWG31MaxHandshakeAttempts, want: "0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.value.String(); got != tt.want {
				t.Fatalf("range = %q, want %q", got, tt.want)
			}
		})
	}

	if cfg.AWG31RandomTrailers != "off" {
		t.Fatalf("AWG31RandomTrailers = %q, want off", cfg.AWG31RandomTrailers)
	}
	if cfg.AWG31DisableCookies != "on" {
		t.Fatalf("AWG31DisableCookies = %q, want on", cfg.AWG31DisableCookies)
	}
}

func TestLoadNormalizesSupportedDefaultProtocolVersions(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "2", want: "2.0"},
		{input: "2.0", want: "2.0"},
		{input: "3.1", want: "3.1"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			setRequiredConfigEnvironment(t)
			t.Setenv("AWG_DEFAULT_PROTOCOL_VERSION", tt.input)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.DefaultProtocolVersion != tt.want {
				t.Fatalf("DefaultProtocolVersion = %q, want %q", cfg.DefaultProtocolVersion, tt.want)
			}
		})
	}
}

func TestLoadAWG31RejectsMalformedEnvironment(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "protocol version", key: "AWG_DEFAULT_PROTOCOL_VERSION", value: "3"},
		{name: "MTU", key: "AWG31_MTU", value: "1279"},
		{name: "persistent keepalive", key: "AWG31_PERSISTENT_KEEPALIVE", value: "1-"},
		{name: "content padding addition", key: "AWG31_CONTENT_PADDING_ADDITION", value: "1-"},
		{name: "rekey after time", key: "AWG31_REKEY_AFTER_TIME", value: "1-"},
		{name: "rekey timeout", key: "AWG31_REKEY_TIMEOUT", value: "1-"},
		{name: "reject after time", key: "AWG31_REJECT_AFTER_TIME", value: "1-"},
		{name: "keepalive timeout", key: "AWG31_KEEPALIVE_TIMEOUT", value: "1-"},
		{name: "max handshake attempts", key: "AWG31_MAX_HANDSHAKE_ATTEMPTS", value: "1-"},
		{name: "random trailers", key: "AWG31_RANDOM_TRAILERS", value: "enabled"},
		{name: "disable cookies", key: "AWG31_DISABLE_COOKIES", value: "disabled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredConfigEnvironment(t)
			t.Setenv(tt.key, tt.value)

			_, err := Load()
			if err == nil {
				t.Fatalf("Load() succeeded with %s=%q", tt.key, tt.value)
			}
			if !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("Load() error = %q, want field-specific %s error", err, tt.key)
			}
		})
	}
}

func TestLoadPreservesLegacyEnvIntFallback(t *testing.T) {
	setRequiredConfigEnvironment(t)
	t.Setenv("AWG_LISTEN_PORT", "not-a-number")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenPort != 51820 {
		t.Fatalf("ListenPort = %d, want legacy fallback 51820", cfg.ListenPort)
	}
}

func setRequiredConfigEnvironment(t *testing.T) {
	t.Helper()

	for _, key := range []string{
		"AWG_DEFAULT_PROTOCOL_VERSION",
		"AWG31_MTU",
		"AWG31_PERSISTENT_KEEPALIVE",
		"AWG31_CONTENT_PADDING_ADDITION",
		"AWG31_REKEY_AFTER_TIME",
		"AWG31_REKEY_TIMEOUT",
		"AWG31_REJECT_AFTER_TIME",
		"AWG31_KEEPALIVE_TIMEOUT",
		"AWG31_MAX_HANDSHAKE_ATTEMPTS",
		"AWG31_RANDOM_TRAILERS",
		"AWG31_DISABLE_COOKIES",
	} {
		t.Setenv(key, "")
	}

	t.Setenv("AWG_API_TOKEN", "synthetic-test-token")
	t.Setenv("AWG_ADDRESS", "10.22.0.1/24")
	t.Setenv("AWG_ENDPOINT", "vpn.example.test")
}
