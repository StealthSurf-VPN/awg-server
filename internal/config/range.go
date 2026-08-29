package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type uint16RangeKind uint8

const (
	uint16RangeScalar uint16RangeKind = iota
	uint16RangeSpan
	uint16RangeOff
)

type Uint16Range struct {
	kind  uint16RangeKind
	start uint16
	end   uint16
}

func ParseUint16Range(value string) (Uint16Range, error) {
	if value == "off" {
		return Uint16Range{kind: uint16RangeOff}, nil
	}

	if strings.Count(value, "-") == 0 {
		scalar, err := parseUint16Decimal(value)
		if err != nil {
			return Uint16Range{}, fmt.Errorf("parse unsigned 16-bit range: %w", err)
		}

		return Uint16Range{kind: uint16RangeScalar, start: scalar, end: scalar}, nil
	}

	if strings.Count(value, "-") != 1 {
		return Uint16Range{}, fmt.Errorf("parse unsigned 16-bit range: malformed range")
	}

	parts := strings.Split(value, "-")
	start, err := parseUint16Decimal(parts[0])
	if err != nil {
		return Uint16Range{}, fmt.Errorf("parse unsigned 16-bit range start: %w", err)
	}

	end, err := parseUint16Decimal(parts[1])
	if err != nil {
		return Uint16Range{}, fmt.Errorf("parse unsigned 16-bit range end: %w", err)
	}

	if start > end {
		return Uint16Range{}, fmt.Errorf("parse unsigned 16-bit range: start must not exceed end")
	}
	return Uint16Range{kind: uint16RangeSpan, start: start, end: end}, nil
}

func (value Uint16Range) IsScalar() bool {
	// Keep the input form distinct until version-specific validation rejects
	// range syntax and the off alias for legacy protocol clients.
	return value.kind == uint16RangeScalar
}

func (value Uint16Range) IsCanonical() bool {
	return value.kind == uint16RangeScalar || value.kind == uint16RangeSpan && value.start != value.end
}

func (value Uint16Range) Canonical() Uint16Range {
	if value.IsCanonical() {
		return value
	}

	return Uint16Range{kind: uint16RangeScalar, start: value.start, end: value.start}
}

func (value Uint16Range) Scalar() (uint16, bool) {
	if !value.IsScalar() {
		return 0, false
	}

	return value.start, true
}

func (value Uint16Range) String() string {
	canonical := value.Canonical()

	switch canonical.kind {
	case uint16RangeSpan:
		return fmt.Sprintf("%d-%d", canonical.start, canonical.end)
	default:
		return strconv.FormatUint(uint64(canonical.start), 10)
	}
}

func (value Uint16Range) MarshalJSON() ([]byte, error) {
	canonical := value.Canonical()
	if canonical.IsScalar() {
		return []byte(canonical.String()), nil
	}

	return json.Marshal(canonical.String())
}

func (value *Uint16Range) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return fmt.Errorf("unsigned 16-bit range cannot be null")
	}

	var parsed Uint16Range
	var err error

	if len(data) > 0 && data[0] == '"' {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return fmt.Errorf("decode unsigned 16-bit range: %w", err)
		}

		parsed, err = ParseUint16Range(text)
	} else {
		parsed, err = ParseUint16Range(string(data))
	}
	if err != nil {
		return err
	}

	*value = parsed

	return nil
}

func parseUint16Decimal(value string) (uint16, error) {
	if value == "" {
		return 0, fmt.Errorf("empty decimal value")
	}

	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, fmt.Errorf("non-decimal value")
		}
	}

	parsed, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		return 0, err
	}

	return uint16(parsed), nil
}
