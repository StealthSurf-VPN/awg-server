package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

type Config struct {
	APIToken                    string
	Address                     string
	Endpoint                    string
	ListenPort                  int
	HTTPPort                    int
	MTU                         int
	DNS                         string
	DataDir                     string
	Interface                   string
	DefaultProtocolVersion      string
	AWG31MTU                    int
	AWG31PersistentKeepalive    Uint16Range
	AWG31ContentPaddingAddition Uint16Range
	AWG31RekeyAfterTime         Uint16Range
	AWG31RekeyTimeout           Uint16Range
	AWG31RejectAfterTime        Uint16Range
	AWG31KeepaliveTimeout       Uint16Range
	AWG31MaxHandshakeAttempts   Uint16Range
	AWG31RandomTrailers         string
	AWG31DisableCookies         string

	Jc   int
	Jmin int
	Jmax int
	S3   int
	S4   int

	I1 string
	I2 string
	I3 string
	I4 string
	I5 string

	MaxInterfaces int
}

type awg31Settings struct {
	defaultProtocolVersion string
	mtu                    int
	persistentKeepalive    Uint16Range
	contentPaddingAddition Uint16Range
	rekeyAfterTime         Uint16Range
	rekeyTimeout           Uint16Range
	rejectAfterTime        Uint16Range
	keepaliveTimeout       Uint16Range
	maxHandshakeAttempts   Uint16Range
	randomTrailers         string
	disableCookies         string
}

func Load() (*Config, error) {
	awg31, err := loadAWG31Settings()
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		APIToken:                    os.Getenv("AWG_API_TOKEN"),
		Address:                     os.Getenv("AWG_ADDRESS"),
		Endpoint:                    os.Getenv("AWG_ENDPOINT"),
		ListenPort:                  envInt("AWG_LISTEN_PORT", 51820),
		HTTPPort:                    envInt("AWG_HTTP_PORT", 7777),
		MTU:                         envInt("AWG_MTU", 1420),
		DNS:                         envDefault("AWG_DNS", "1.1.1.1"),
		DataDir:                     envDefault("AWG_DATA_DIR", "/data"),
		Interface:                   os.Getenv("AWG_INTERFACE"),
		DefaultProtocolVersion:      awg31.defaultProtocolVersion,
		AWG31MTU:                    awg31.mtu,
		AWG31PersistentKeepalive:    awg31.persistentKeepalive,
		AWG31ContentPaddingAddition: awg31.contentPaddingAddition,
		AWG31RekeyAfterTime:         awg31.rekeyAfterTime,
		AWG31RekeyTimeout:           awg31.rekeyTimeout,
		AWG31RejectAfterTime:        awg31.rejectAfterTime,
		AWG31KeepaliveTimeout:       awg31.keepaliveTimeout,
		AWG31MaxHandshakeAttempts:   awg31.maxHandshakeAttempts,
		AWG31RandomTrailers:         awg31.randomTrailers,
		AWG31DisableCookies:         awg31.disableCookies,

		Jc:   envInt("AWG_JC", 5),
		Jmin: envInt("AWG_JMIN", 50),
		Jmax: envInt("AWG_JMAX", 1000),
		S3:   envInt("AWG_S3", 0),
		S4:   envInt("AWG_S4", 0),

		I1: os.Getenv("AWG_I1"),
		I2: os.Getenv("AWG_I2"),
		I3: os.Getenv("AWG_I3"),
		I4: os.Getenv("AWG_I4"),
		I5: os.Getenv("AWG_I5"),

		MaxInterfaces: envInt("AWG_MAX_INTERFACES", 0),
	}

	if cfg.APIToken == "" {
		return nil, fmt.Errorf("AWG_API_TOKEN is required")
	}

	if cfg.Address == "" {
		return nil, fmt.Errorf("AWG_ADDRESS is required")
	}

	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("AWG_ENDPOINT is required")
	}

	ip, _, err := net.ParseCIDR(cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("AWG_ADDRESS must be a valid CIDR (e.g. 10.0.0.1/24): %w", err)
	}

	if ip.To4() == nil {
		return nil, fmt.Errorf("AWG_ADDRESS must be an IPv4 CIDR, got: %s", cfg.Address)
	}

	return cfg, nil
}

func loadAWG31Settings() (awg31Settings, error) {
	defaultProtocolVersion, err := envProtocolVersion("AWG_DEFAULT_PROTOCOL_VERSION", "3.1")
	if err != nil {
		return awg31Settings{}, err
	}

	mtu, err := envMTU("AWG31_MTU", 1280)
	if err != nil {
		return awg31Settings{}, err
	}

	persistentKeepalive, err := envUint16Range("AWG31_PERSISTENT_KEEPALIVE", "25-35")
	if err != nil {
		return awg31Settings{}, err
	}

	contentPaddingAddition, err := envUint16Range("AWG31_CONTENT_PADDING_ADDITION", "10-100")
	if err != nil {
		return awg31Settings{}, err
	}

	rekeyAfterTime, err := envUint16Range("AWG31_REKEY_AFTER_TIME", "100-120")
	if err != nil {
		return awg31Settings{}, err
	}

	rekeyTimeout, err := envUint16Range("AWG31_REKEY_TIMEOUT", "3-7")
	if err != nil {
		return awg31Settings{}, err
	}

	rejectAfterTime, err := envUint16Range("AWG31_REJECT_AFTER_TIME", "150-180")
	if err != nil {
		return awg31Settings{}, err
	}

	keepaliveTimeout, err := envUint16Range("AWG31_KEEPALIVE_TIMEOUT", "5-15")
	if err != nil {
		return awg31Settings{}, err
	}

	maxHandshakeAttempts, err := envUint16Range("AWG31_MAX_HANDSHAKE_ATTEMPTS", "15-20")
	if err != nil {
		return awg31Settings{}, err
	}

	randomTrailers, err := envToggle("AWG31_RANDOM_TRAILERS", "on")
	if err != nil {
		return awg31Settings{}, err
	}

	disableCookies, err := envToggle("AWG31_DISABLE_COOKIES", "off")
	if err != nil {
		return awg31Settings{}, err
	}

	return awg31Settings{
		defaultProtocolVersion: defaultProtocolVersion,
		mtu:                    mtu,
		persistentKeepalive:    persistentKeepalive,
		contentPaddingAddition: contentPaddingAddition,
		rekeyAfterTime:         rekeyAfterTime,
		rekeyTimeout:           rekeyTimeout,
		rejectAfterTime:        rejectAfterTime,
		keepaliveTimeout:       keepaliveTimeout,
		maxHandshakeAttempts:   maxHandshakeAttempts,
		randomTrailers:         randomTrailers,
		disableCookies:         disableCookies,
	}, nil
}

func (c *Config) ServerIP() net.IP {
	ip, _, _ := net.ParseCIDR(c.Address)
	return ip
}

func (c *Config) Network() *net.IPNet {
	_, network, _ := net.ParseCIDR(c.Address)
	return network
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}

	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}

	return n
}

func envProtocolVersion(key, fallback string) (string, error) {
	switch value := envDefault(key, fallback); value {
	case "2", "2.0":
		return "2.0", nil
	case "3.1":
		return "3.1", nil
	default:
		return "", fmt.Errorf("%s must be one of 2, 2.0, or 3.1", key)
	}
}

func envMTU(key string, fallback uint16) (int, error) {
	value, err := parseUint16Decimal(envDefault(key, strconv.Itoa(int(fallback))))
	if err != nil || value < 1280 || value > 1420 {
		return 0, fmt.Errorf("%s must be an integer between 1280 and 1420", key)
	}

	return int(value), nil
}

func envUint16Range(key, fallback string) (Uint16Range, error) {
	value, err := ParseUint16Range(envDefault(key, fallback))
	if err != nil {
		return Uint16Range{}, fmt.Errorf("%s must be an unsigned 16-bit scalar, range, or off alias: %w", key, err)
	}

	return value.Canonical(), nil
}

func envToggle(key, fallback string) (string, error) {
	switch value := envDefault(key, fallback); value {
	case "on", "off":
		return value, nil
	default:
		return "", fmt.Errorf("%s must be on or off", key)
	}
}
