package awg

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"strconv"

	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

const MinPort = 1024
const MaxPort = 65535
const MinMTU = 1280
const MaxMTU = 1420
const DefaultPersistentKeepalive = 25
const MaxPersistentKeepalive = 65535

var ErrInvalidPort = fmt.Errorf("port must be 0 or between %d and %d", MinPort, MaxPort)
var ErrInvalidPersistentKeepalive = fmt.Errorf("persistent_keepalive must be between 0 and %d", MaxPersistentKeepalive)

type AWGParams struct {
	Port             int      `json:"port,omitempty"`
	ClientListenPort int      `json:"client_listen_port,omitempty"`
	MTU              int      `json:"mtu,omitempty"`
	DNS              string   `json:"dns,omitempty"`
	DNSMode          string   `json:"dns_mode,omitempty"`
	DNSServers       []string `json:"dns_servers,omitempty"`

	dnsSet        bool
	dnsModeSet    bool
	dnsServersSet bool

	PersistentKeepalive    *config.Uint16Range `json:"persistent_keepalive,omitempty"`
	ContentPaddingAddition *config.Uint16Range `json:"content_padding_addition,omitempty"`
	RekeyAfterTime         *config.Uint16Range `json:"rekey_after_time,omitempty"`
	RekeyTimeout           *config.Uint16Range `json:"rekey_timeout,omitempty"`
	RejectAfterTime        *config.Uint16Range `json:"reject_after_time,omitempty"`
	KeepaliveTimeout       *config.Uint16Range `json:"keepalive_timeout,omitempty"`
	MaxHandshakeAttempts   *config.Uint16Range `json:"max_handshake_attempts,omitempty"`
	RandomTrailers         string              `json:"random_trailers,omitempty"`
	DisableCookies         string              `json:"disable_cookies,omitempty"`
	Jc                     int                 `json:"jc,omitempty"`
	Jmin                   int                 `json:"jmin,omitempty"`
	Jmax                   int                 `json:"jmax,omitempty"`
	S1                     int                 `json:"s1,omitempty"`
	S2                     int                 `json:"s2,omitempty"`
	S3                     int                 `json:"s3,omitempty"`
	S4                     int                 `json:"s4,omitempty"`
	H1                     string              `json:"h1,omitempty"`
	H2                     string              `json:"h2,omitempty"`
	H3                     string              `json:"h3,omitempty"`
	H4                     string              `json:"h4,omitempty"`
	I1                     string              `json:"i1,omitempty"`
	I2                     string              `json:"i2,omitempty"`
	I3                     string              `json:"i3,omitempty"`
	I4                     string              `json:"i4,omitempty"`
	I5                     string              `json:"i5,omitempty"`
}

func ValidatePort(port int) error {
	if port == 0 {
		return nil
	}

	if port < MinPort || port > MaxPort {
		return ErrInvalidPort
	}

	return nil
}

func ValidateMTU(mtu int) error {
	if mtu == 0 {
		return nil
	}

	if mtu < MinMTU || mtu > MaxMTU {
		return fmt.Errorf("mtu must be between %d and %d", MinMTU, MaxMTU)
	}

	return nil
}

func ValidatePersistentKeepalive(value *config.Uint16Range) error {
	if value == nil {
		return nil
	}

	return nil
}

func (p AWGParams) PersistentKeepaliveValue() int {
	if p.PersistentKeepalive == nil {
		return DefaultPersistentKeepalive
	}

	value, ok := p.PersistentKeepalive.Scalar()
	if !ok {
		return DefaultPersistentKeepalive
	}

	return int(value)
}

func (p AWGParams) PersistentKeepaliveConfigValue(version ProtocolVersion) string {
	if p.PersistentKeepalive != nil {
		return p.PersistentKeepalive.String()
	}

	if version == ProtocolVersion2 {
		return strconv.Itoa(DefaultPersistentKeepalive)
	}

	return ""
}

func (p AWGParams) Key() string {
	return fmt.Sprintf(
		"h1=%s,h2=%s,h3=%s,h4=%s,s1=%d,s2=%d,s3=%d,s4=%d",
		p.H1, p.H2, p.H3, p.H4,
		p.S1, p.S2, p.S3, p.S4,
	)
}

func (p AWGParams) CLIArgs() []string {
	var args []string

	if p.Jc > 0 {
		args = append(args, "jc", fmt.Sprintf("%d", p.Jc))
	}

	if p.Jmin > 0 {
		args = append(args, "jmin", fmt.Sprintf("%d", p.Jmin))
	}

	if p.Jmax > 0 {
		args = append(args, "jmax", fmt.Sprintf("%d", p.Jmax))
	}

	if p.S1 > 0 {
		args = append(args, "s1", fmt.Sprintf("%d", p.S1))
	}

	if p.S2 > 0 {
		args = append(args, "s2", fmt.Sprintf("%d", p.S2))
	}

	args = append(args, "s3", fmt.Sprintf("%d", p.S3))
	args = append(args, "s4", fmt.Sprintf("%d", p.S4))

	if p.H1 != "" {
		args = append(args, "h1", p.H1)
	}

	if p.H2 != "" {
		args = append(args, "h2", p.H2)
	}

	if p.H3 != "" {
		args = append(args, "h3", p.H3)
	}

	if p.H4 != "" {
		args = append(args, "h4", p.H4)
	}

	args = appendRangeCLIArgs(args, "content-padding-addition", p.ContentPaddingAddition)
	args = appendRangeCLIArgs(args, "rekey-after-time", p.RekeyAfterTime)
	args = appendRangeCLIArgs(args, "rekey-timeout", p.RekeyTimeout)
	args = appendRangeCLIArgs(args, "reject-after-time", p.RejectAfterTime)
	args = appendRangeCLIArgs(args, "keepalive-timeout", p.KeepaliveTimeout)
	args = appendRangeCLIArgs(args, "max-handshake-attempts", p.MaxHandshakeAttempts)

	if p.RandomTrailers != "" {
		args = append(args, "random-trailers", p.RandomTrailers)
	}

	if p.DisableCookies != "" {
		args = append(args, "disable-cookies", p.DisableCookies)
	}

	return args
}

func (p AWGParams) ConfigLines() string {
	lines := p.serverConfigLines()

	if p.I1 != "" {
		lines += fmt.Sprintf("\nI1 = %s", p.I1)
	}

	if p.I2 != "" {
		lines += fmt.Sprintf("\nI2 = %s", p.I2)
	}

	if p.I3 != "" {
		lines += fmt.Sprintf("\nI3 = %s", p.I3)
	}

	if p.I4 != "" {
		lines += fmt.Sprintf("\nI4 = %s", p.I4)
	}

	if p.I5 != "" {
		lines += fmt.Sprintf("\nI5 = %s", p.I5)
	}

	return lines
}

func (p AWGParams) serverConfigLines() string {
	var lines string

	if p.Jc > 0 {
		lines += fmt.Sprintf("\nJc = %d", p.Jc)
	}

	if p.Jmin > 0 {
		lines += fmt.Sprintf("\nJmin = %d", p.Jmin)
	}

	if p.Jmax > 0 {
		lines += fmt.Sprintf("\nJmax = %d", p.Jmax)
	}

	if p.S1 > 0 {
		lines += fmt.Sprintf("\nS1 = %d", p.S1)
	}

	if p.S2 > 0 {
		lines += fmt.Sprintf("\nS2 = %d", p.S2)
	}

	lines += fmt.Sprintf("\nS3 = %d", p.S3)
	lines += fmt.Sprintf("\nS4 = %d", p.S4)

	if p.H1 != "" {
		lines += fmt.Sprintf("\nH1 = %s", p.H1)
	}

	if p.H2 != "" {
		lines += fmt.Sprintf("\nH2 = %s", p.H2)
	}

	if p.H3 != "" {
		lines += fmt.Sprintf("\nH3 = %s", p.H3)
	}

	if p.H4 != "" {
		lines += fmt.Sprintf("\nH4 = %s", p.H4)
	}

	lines = appendRangeConfigLine(lines, "ContentPaddingAddition", p.ContentPaddingAddition)
	lines = appendRangeConfigLine(lines, "RekeyAfterTime", p.RekeyAfterTime)
	lines = appendRangeConfigLine(lines, "RekeyTimeout", p.RekeyTimeout)
	lines = appendRangeConfigLine(lines, "RejectAfterTime", p.RejectAfterTime)
	lines = appendRangeConfigLine(lines, "KeepaliveTimeout", p.KeepaliveTimeout)
	lines = appendRangeConfigLine(lines, "MaxHandshakeAttempts", p.MaxHandshakeAttempts)

	if p.RandomTrailers != "" {
		lines += fmt.Sprintf("\nRandomTrailers = %s", p.RandomTrailers)
	}

	if p.DisableCookies != "" {
		lines += fmt.Sprintf("\nDisableCookies = %s", p.DisableCookies)
	}

	return lines
}

type GeneratedParams struct {
	H1 string `json:"h1"`
	H2 string `json:"h2"`
	H3 string `json:"h3"`
	H4 string `json:"h4"`
	S1 int    `json:"s1"`
	S2 int    `json:"s2"`
}

type GeneratedParamsV31 struct {
	H1 string `json:"h1"`
	H2 string `json:"h2"`
	H3 string `json:"h3"`
	H4 string `json:"h4"`
	S1 int    `json:"s1"`
	S2 int    `json:"s2"`
	S3 int    `json:"s3"`
	S4 int    `json:"s4"`
}

func GenerateParams() (*GeneratedParams, error) {
	h1, err := generateHRange(100_000, 800_000)
	if err != nil {
		return nil, fmt.Errorf("generate h1: %w", err)
	}

	h2, err := generateHRange(1_000_000, 8_000_000)
	if err != nil {
		return nil, fmt.Errorf("generate h2: %w", err)
	}

	h3, err := generateHRange(10_000_000, 80_000_000)
	if err != nil {
		return nil, fmt.Errorf("generate h3: %w", err)
	}

	h4, err := generateHRange(100_000_000, 800_000_000)
	if err != nil {
		return nil, fmt.Errorf("generate h4: %w", err)
	}

	s1, err := randIntRange(15, 151)
	if err != nil {
		return nil, fmt.Errorf("generate s1: %w", err)
	}

	var s2 int

	for {
		s2, err = randIntRange(15, 151)
		if err != nil {
			return nil, fmt.Errorf("generate s2: %w", err)
		}

		if s1+56 != s2 {
			break
		}
	}

	return &GeneratedParams{
		H1: h1, H2: h2, H3: h3, H4: h4,
		S1: s1, S2: s2,
	}, nil
}

func ApplyGeneratedParams(params *AWGParams, generated GeneratedParams) *AWGParams {
	result := cloneAWGParams(params)
	if result == nil {
		result = &AWGParams{}
	}

	result.H1 = generated.H1
	result.H2 = generated.H2
	result.H3 = generated.H3
	result.H4 = generated.H4
	result.S1 = generated.S1
	result.S2 = generated.S2

	return result
}

func GenerateParamsV31() (*GeneratedParamsV31, error) {
	h1, err := generateHValue(100_000, 800_000)
	if err != nil {
		return nil, fmt.Errorf("generate h1: %w", err)
	}

	h2, err := generateHValue(1_000_000, 8_000_000)
	if err != nil {
		return nil, fmt.Errorf("generate h2: %w", err)
	}

	h3, err := generateHValue(10_000_000, 80_000_000)
	if err != nil {
		return nil, fmt.Errorf("generate h3: %w", err)
	}

	h4, err := generateHValue(100_000_000, 800_000_000)
	if err != nil {
		return nil, fmt.Errorf("generate h4: %w", err)
	}

	s1, err := randIntRange(15, 151)
	if err != nil {
		return nil, fmt.Errorf("generate s1: %w", err)
	}

	var s2 int

	for {
		s2, err = randIntRange(15, 151)
		if err != nil {
			return nil, fmt.Errorf("generate s2: %w", err)
		}

		if s1+56 != s2 {
			break
		}
	}

	s3, err := randIntRange(15, 64)
	if err != nil {
		return nil, fmt.Errorf("generate s3: %w", err)
	}

	return &GeneratedParamsV31{
		H1: h1, H2: h2, H3: h3, H4: h4,
		S1: s1, S2: s2, S3: s3, S4: 12,
	}, nil
}

func ApplyGeneratedParamsV31(params *AWGParams, generated GeneratedParamsV31) *AWGParams {
	result := cloneAWGParams(params)
	if result == nil {
		result = &AWGParams{}
	}

	result.H1 = generated.H1
	result.H2 = generated.H2
	result.H3 = generated.H3
	result.H4 = generated.H4
	result.S1 = generated.S1
	result.S2 = generated.S2
	result.S3 = generated.S3
	result.S4 = generated.S4

	return result
}

func generateHRange(tierMin, tierMax uint32) (string, error) {
	mid := tierMin + (tierMax-tierMin)/2

	lo, err := randUint32Range(tierMin, mid)
	if err != nil {
		return "", err
	}

	hi, err := randUint32Range(mid, tierMax)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%d-%d", lo, hi), nil
}

func generateHValue(tierMin, tierMax uint32) (string, error) {
	value, err := randUint32Range(tierMin, tierMax)
	if err != nil {
		return "", err
	}

	return strconv.FormatUint(uint64(value), 10), nil
}

func randUint32Range(min, max uint32) (uint32, error) {
	var buf [4]byte

	if _, err := rand.Read(buf[:]); err != nil {
		return 0, err
	}

	n := binary.LittleEndian.Uint32(buf[:])

	return min + n%(max-min), nil
}

func randIntRange(min, max int) (int, error) {
	var buf [4]byte

	if _, err := rand.Read(buf[:]); err != nil {
		return 0, err
	}

	n := binary.LittleEndian.Uint32(buf[:])

	return min + int(n)%(max-min), nil
}

func appendRangeCLIArgs(args []string, name string, value *config.Uint16Range) []string {
	if value == nil {
		return args
	}

	return append(args, name, value.String())
}

func appendRangeConfigLine(lines, name string, value *config.Uint16Range) string {
	if value == nil {
		return lines
	}

	return fmt.Sprintf("%s\n%s = %s", lines, name, value.String())
}
