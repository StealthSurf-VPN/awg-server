package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
)

type Config struct {
	APIToken   string
	Address    string
	Endpoint   string
	ListenPort int
	HTTPPort   int
	MTU        int
	DNS        string
	DataDir    string
	Interface  string

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

func Load() (*Config, error) {
	cfg := &Config{
		APIToken:   os.Getenv("AWG_API_TOKEN"),
		Address:    os.Getenv("AWG_ADDRESS"),
		Endpoint:   os.Getenv("AWG_ENDPOINT"),
		ListenPort: envInt("AWG_LISTEN_PORT", 51820),
		HTTPPort:   envInt("AWG_HTTP_PORT", 7777),
		MTU:        envInt("AWG_MTU", 1420),
		DNS:        envDefault("AWG_DNS", "1.1.1.1"),
		DataDir:    envDefault("AWG_DATA_DIR", "/data"),
		Interface:  os.Getenv("AWG_INTERFACE"),

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

