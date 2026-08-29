package awg

import "fmt"

type ProtocolVersion string

const (
	ProtocolVersion2  ProtocolVersion = "2.0"
	ProtocolVersion31 ProtocolVersion = "3.1"
)

func ParseProtocolVersion(value string) (ProtocolVersion, error) {
	switch value {
	case "2", string(ProtocolVersion2):
		return ProtocolVersion2, nil
	case string(ProtocolVersion31):
		return ProtocolVersion31, nil
	default:
		return "", fmt.Errorf("unsupported protocol version %q", value)
	}
}

func (version ProtocolVersion) Valid() bool {
	return version == ProtocolVersion2 || version == ProtocolVersion31
}

func (version ProtocolVersion) String() string {
	return string(version)
}
