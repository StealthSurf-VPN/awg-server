package clients

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

const RoutingModeFull = "full"
const RoutingModeSplit = "split"
const fullTunnelAllowedIPs = "0.0.0.0/0, ::/0"

var ErrInvalidRouting = errors.New("invalid routing")

type Routing struct {
	Mode       string   `json:"mode"`
	AllowedIPs []string `json:"allowed_ips,omitempty"`
}

type RoutingValidationError struct {
	Field  string
	Reason string
}

func (e *RoutingValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Reason)
}

func (e *RoutingValidationError) Unwrap() error {
	return ErrInvalidRouting
}

func NormalizeRouting(routing *Routing) (*Routing, error) {
	if routing == nil {
		return nil, nil
	}

	switch routing.Mode {
	case RoutingModeFull:
		if len(routing.AllowedIPs) > 0 {
			return nil, invalidRouting("routing.allowed_ips", `must be empty when mode is "full"`)
		}

		return nil, nil
	case RoutingModeSplit:
		if len(routing.AllowedIPs) == 0 {
			return nil, invalidRouting("routing.allowed_ips", `must contain at least one IPv4 CIDR when mode is "split"`)
		}
	default:
		return nil, invalidRouting("routing.mode", `must be "full" or "split"`)
	}

	allowedIPs := make([]string, 0, len(routing.AllowedIPs))
	seen := make(map[string]bool, len(routing.AllowedIPs))

	for i, value := range routing.AllowedIPs {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || !prefix.Addr().Is4() {
			return nil, invalidRouting(fmt.Sprintf("routing.allowed_ips[%d]", i), "must be a valid IPv4 CIDR")
		}

		normalized := prefix.Masked().String()
		if seen[normalized] {
			continue
		}

		seen[normalized] = true
		allowedIPs = append(allowedIPs, normalized)
	}

	return &Routing{
		Mode:       RoutingModeSplit,
		AllowedIPs: allowedIPs,
	}, nil
}

func EffectiveRouting(routing *Routing) Routing {
	if routing == nil {
		return Routing{Mode: RoutingModeFull}
	}

	return Routing{
		Mode:       routing.Mode,
		AllowedIPs: append([]string(nil), routing.AllowedIPs...),
	}
}

func routingAllowedIPs(routing *Routing) string {
	if routing == nil || routing.Mode == RoutingModeFull {
		return fullTunnelAllowedIPs
	}

	return strings.Join(routing.AllowedIPs, ", ")
}

func invalidRouting(field, reason string) error {
	return &RoutingValidationError{
		Field:  field,
		Reason: reason,
	}
}
