package awg

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const MaxJunkPacketCount = 128
const MaxJunkPacketSize = 1280
const MaxS1 = 1132
const MaxS2 = 1188
const MaxS3 = 64
const MaxS4 = 32
const MaxCPSTagSize = 1000
const MaxCPSPacketSize = 1280

var ErrInvalidParams = errors.New("invalid awg params")

type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

func (e *ValidationError) Unwrap() error {
	return ErrInvalidParams
}

type headerRange struct {
	start uint32
	end   uint32
}

func ValidateOverrides(params *AWGParams) error {
	return ValidateOverridesForVersion(ProtocolVersion2, params)
}

func ValidateOverridesForVersion(version ProtocolVersion, params *AWGParams) error {
	if !version.Valid() {
		return invalidParam("protocol_version", "must be 2.0 or 3.1")
	}

	if params == nil {
		return nil
	}

	if err := ValidatePort(params.Port); err != nil {
		return validationFrom("port", err)
	}

	if params.ClientListenPort != 0 && (params.ClientListenPort < MinPort || params.ClientListenPort > MaxPort) {
		return invalidParam("client_listen_port", "must be 0 or between %d and %d", MinPort, MaxPort)
	}

	if err := ValidateMTU(params.MTU); err != nil {
		return validationFrom("mtu", err)
	}

	if err := validateDNSSettings(params); err != nil {
		return err
	}

	if err := ValidatePersistentKeepalive(params.PersistentKeepalive); err != nil {
		return validationFrom("persistent_keepalive", err)
	}
	if version == ProtocolVersion2 && params.PersistentKeepalive != nil && !params.PersistentKeepalive.IsScalar() {
		return invalidParam("persistent_keepalive", "must be a scalar for protocol version 2.0")
	}

	if version == ProtocolVersion2 {
		for _, value := range []struct {
			field string
			set   bool
		}{
			{field: "content_padding_addition", set: params.ContentPaddingAddition != nil},
			{field: "rekey_after_time", set: params.RekeyAfterTime != nil},
			{field: "rekey_timeout", set: params.RekeyTimeout != nil},
			{field: "reject_after_time", set: params.RejectAfterTime != nil},
			{field: "keepalive_timeout", set: params.KeepaliveTimeout != nil},
			{field: "max_handshake_attempts", set: params.MaxHandshakeAttempts != nil},
			{field: "random_trailers", set: params.RandomTrailers != ""},
			{field: "disable_cookies", set: params.DisableCookies != ""},
		} {
			if value.set {
				return invalidParam(value.field, "is only supported for protocol version 3.1")
			}
		}
	} else {
		if err := validateToggle("random_trailers", params.RandomTrailers); err != nil {
			return err
		}
		if err := validateToggle("disable_cookies", params.DisableCookies); err != nil {
			return err
		}
	}

	for _, value := range []struct {
		field string
		value int
		max   int
	}{
		{field: "jc", value: params.Jc, max: MaxJunkPacketCount},
		{field: "jmin", value: params.Jmin, max: MaxJunkPacketSize},
		{field: "jmax", value: params.Jmax, max: MaxJunkPacketSize},
		{field: "s1", value: params.S1, max: MaxS1},
		{field: "s2", value: params.S2, max: MaxS2},
		{field: "s3", value: params.S3, max: MaxS3},
		{field: "s4", value: params.S4, max: MaxS4},
	} {
		if value.value < 0 || value.value > value.max {
			return invalidParam(value.field, "must be between 0 and %d", value.max)
		}
	}

	for _, value := range []struct {
		field string
		value string
	}{
		{field: "h1", value: params.H1},
		{field: "h2", value: params.H2},
		{field: "h3", value: params.H3},
		{field: "h4", value: params.H4},
	} {
		if value.value == "" {
			continue
		}

		if _, err := parseHeaderRange(value.field, value.value); err != nil {
			return err
		}
	}

	for _, value := range []struct {
		field string
		value string
	}{
		{field: "i1", value: params.I1},
		{field: "i2", value: params.I2},
		{field: "i3", value: params.I3},
		{field: "i4", value: params.I4},
		{field: "i5", value: params.I5},
	} {
		if value.value == "" {
			continue
		}

		if err := validateCPS(value.field, value.value); err != nil {
			return err
		}
	}

	return nil
}

func ValidateProfile(params AWGParams) error {
	return ValidateProfileForVersion(ProtocolVersion2, params)
}

func ValidateProfileForVersion(version ProtocolVersion, params AWGParams) error {
	if err := ValidateOverridesForVersion(version, &params); err != nil {
		return err
	}

	if version == ProtocolVersion31 {
		for _, value := range []struct {
			field string
			value int
		}{
			{field: "s1", value: params.S1},
			{field: "s2", value: params.S2},
			{field: "s3", value: params.S3},
			{field: "s4", value: params.S4},
		} {
			if value.value < 12 {
				return invalidParam(value.field, "must be at least 12 for protocol version 3.1")
			}
		}
	}

	if params.Jc > 0 {
		if params.Jmin <= 0 {
			return invalidParam("jmin", "must be greater than 0 when jc is enabled")
		}

		if params.Jmax <= 0 {
			return invalidParam("jmax", "must be greater than 0 when jc is enabled")
		}

		if params.Jmin >= params.Jmax {
			return invalidParam("jmin", "must be less than jmax")
		}
	}

	if params.S1+56 == params.S2 {
		return invalidParam("s2", "must not equal s1 + 56")
	}

	headers := []struct {
		field string
		value string
	}{
		{field: "h1", value: params.H1},
		{field: "h2", value: params.H2},
		{field: "h3", value: params.H3},
		{field: "h4", value: params.H4},
	}

	ranges := make([]headerRange, len(headers))

	for i, header := range headers {
		if header.value == "" {
			return invalidParam(header.field, "is required in the effective profile")
		}

		parsed, err := parseHeaderRange(header.field, header.value)
		if err != nil {
			return err
		}

		ranges[i] = parsed
	}

	for i := 0; i < len(ranges); i++ {
		for j := i + 1; j < len(ranges); j++ {
			if ranges[i].end < ranges[j].start || ranges[j].end < ranges[i].start {
				continue
			}

			return invalidParam(headers[j].field, "range must not overlap %s", headers[i].field)
		}
	}

	return nil
}

func parseHeaderRange(field, value string) (headerRange, error) {
	parts := strings.Split(value, "-")
	if len(parts) < 1 || len(parts) > 2 {
		return headerRange{}, invalidParam(field, "must be an unsigned decimal value or start-end range")
	}

	start, err := parseUint32Decimal(parts[0])
	if err != nil {
		return headerRange{}, invalidParam(field, "must be an unsigned decimal value or start-end range")
	}

	end := start
	if len(parts) == 2 {
		end, err = parseUint32Decimal(parts[1])
		if err != nil {
			return headerRange{}, invalidParam(field, "must be an unsigned decimal value or start-end range")
		}
	}

	if start > end {
		return headerRange{}, invalidParam(field, "range start must not exceed range end")
	}

	return headerRange{start: start, end: end}, nil
}

func parseUint32Decimal(value string) (uint32, error) {
	if value == "" {
		return 0, errors.New("empty decimal value")
	}

	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, errors.New("non-decimal value")
		}
	}

	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, err
	}

	return uint32(parsed), nil
}

func validateCPS(field, value string) error {
	totalSize := 0

	for offset := 0; offset < len(value); {
		if value[offset] != '<' {
			return invalidParam(field, "must contain only supported CPS tags")
		}

		relativeEnd := strings.IndexByte(value[offset:], '>')
		if relativeEnd < 0 {
			return invalidParam(field, "contains an unterminated CPS tag")
		}

		end := offset + relativeEnd
		tag := value[offset+1 : end]

		tagSize, err := cpsTagSize(field, tag)
		if err != nil {
			return err
		}

		totalSize += tagSize
		if totalSize > MaxCPSPacketSize {
			return invalidParam(field, "expanded packet size must not exceed %d bytes", MaxCPSPacketSize)
		}

		offset = end + 1
	}

	return nil
}

func cpsTagSize(field, tag string) (int, error) {
	for i := 0; i < len(tag); i++ {
		if tag[i] < 0x20 || tag[i] == 0x7f {
			return 0, invalidParam(field, "contains a control character")
		}
	}

	if tag == "t" {
		return 4, nil
	}

	parts := strings.Split(tag, " ")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, invalidParam(field, "contains a malformed CPS tag")
	}

	switch parts[0] {
	case "b":
		if !strings.HasPrefix(parts[1], "0x") {
			return 0, invalidParam(field, "static byte tags must use the 0x prefix")
		}

		hexValue := parts[1][2:]
		if hexValue == "" || len(hexValue)%2 != 0 {
			return 0, invalidParam(field, "static byte tags require non-empty even-length hex")
		}

		for i := 0; i < len(hexValue); i++ {
			if !isHexDigit(hexValue[i]) {
				return 0, invalidParam(field, "static byte tags contain invalid hex")
			}
		}

		return len(hexValue) / 2, nil
	case "r", "rc", "rd":
		size, err := parseCPSSize(parts[1])
		if err != nil {
			return 0, invalidParam(field, "%s tag size must be between 0 and %d", parts[0], MaxCPSTagSize)
		}

		return size, nil
	default:
		return 0, invalidParam(field, "contains an unsupported CPS tag")
	}
}

func parseCPSSize(value string) (int, error) {
	if value == "" {
		return 0, errors.New("empty size")
	}

	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, errors.New("non-decimal size")
		}
	}

	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil || parsed > MaxCPSTagSize {
		return 0, errors.New("size out of range")
	}

	return int(parsed), nil
}

func isHexDigit(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

func validateToggle(field, value string) error {
	if value == "" || value == "on" || value == "off" {
		return nil
	}

	return invalidParam(field, "must be on or off")
}

func validationFrom(field string, err error) error {
	reason := strings.TrimPrefix(err.Error(), field+" ")
	return &ValidationError{Field: field, Reason: reason}
}

func invalidParam(field, format string, args ...any) error {
	return &ValidationError{Field: field, Reason: fmt.Sprintf(format, args...)}
}
