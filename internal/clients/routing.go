package clients

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
)

const RoutingModeFull = "full"
const RoutingModeSplit = "split"
const RoutingModeBypass = "bypass"
const MaxRoutingListEntries = 4096
const MaxComputedRoutingCIDRs = 16384
const fullTunnelAllowedIPs = "0.0.0.0/0, ::/0"

var ErrInvalidRouting = errors.New("invalid routing")

type Routing struct {
	Mode        string   `json:"mode"`
	AllowedIPs  []string `json:"allowed_ips,omitempty"`
	ExcludedIPs []string `json:"excluded_ips,omitempty"`
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
		if len(routing.ExcludedIPs) > 0 {
			return nil, invalidRouting("routing.excluded_ips", `must be empty when mode is "full"`)
		}

		return nil, nil
	case RoutingModeBypass:
		if len(routing.AllowedIPs) > 0 {
			return nil, invalidRouting("routing.allowed_ips", `must be empty when mode is "bypass"`)
		}
		if len(routing.ExcludedIPs) == 0 {
			return nil, invalidRouting("routing.excluded_ips", `must contain at least one IPv4 CIDR when mode is "bypass"`)
		}
	case RoutingModeSplit:
		if len(routing.AllowedIPs) == 0 {
			return nil, invalidRouting("routing.allowed_ips", `must contain at least one IPv4 CIDR when mode is "split"`)
		}
	default:
		return nil, invalidRouting("routing.mode", `must be "full", "split", or "bypass"`)
	}

	if len(routing.AllowedIPs) > MaxRoutingListEntries {
		return nil, invalidRouting("routing.allowed_ips", fmt.Sprintf("must not contain more than %d entries", MaxRoutingListEntries))
	}
	if len(routing.ExcludedIPs) > MaxRoutingListEntries {
		return nil, invalidRouting("routing.excluded_ips", fmt.Sprintf("must not contain more than %d entries", MaxRoutingListEntries))
	}

	allowedIPs, err := normalizeIPv4Prefixes(routing.AllowedIPs, "routing.allowed_ips")
	if err != nil {
		return nil, err
	}

	excludedIPs, err := normalizeIPv4Prefixes(routing.ExcludedIPs, "routing.excluded_ips")
	if err != nil {
		return nil, err
	}

	normalized := &Routing{
		Mode:        routing.Mode,
		AllowedIPs:  allowedIPs,
		ExcludedIPs: excludedIPs,
	}

	if len(normalized.ExcludedIPs) > 0 {
		if _, err := computedIPv4AllowedIPs(normalized); err != nil {
			return nil, err
		}
	}

	return normalized, nil
}

func EffectiveRouting(routing *Routing) Routing {
	if routing == nil {
		return Routing{Mode: RoutingModeFull}
	}

	return Routing{
		Mode:        routing.Mode,
		AllowedIPs:  append([]string(nil), routing.AllowedIPs...),
		ExcludedIPs: append([]string(nil), routing.ExcludedIPs...),
	}
}

func routingAllowedIPs(routing *Routing) (string, error) {
	if routing == nil || routing.Mode == RoutingModeFull {
		return fullTunnelAllowedIPs, nil
	}

	if routing.Mode == RoutingModeSplit && len(routing.ExcludedIPs) == 0 {
		return strings.Join(routing.AllowedIPs, ", "), nil
	}

	if routing.Mode != RoutingModeSplit && routing.Mode != RoutingModeBypass {
		return "", invalidRouting("routing.mode", `must be "full", "split", or "bypass"`)
	}

	computed, err := computedIPv4AllowedIPs(routing)
	if err != nil {
		return "", err
	}

	allowedIPs := strings.Join(computed, ", ")
	if routing.Mode == RoutingModeBypass {
		allowedIPs += ", ::/0"
	}

	return allowedIPs, nil
}

func normalizeIPv4Prefixes(values []string, field string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}

	normalizedValues := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))

	for i, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || !prefix.Addr().Is4() {
			return nil, invalidRouting(fmt.Sprintf("%s[%d]", field, i), "must be a valid IPv4 CIDR")
		}

		normalized := prefix.Masked().String()
		if _, ok := seen[normalized]; ok {
			continue
		}

		seen[normalized] = struct{}{}
		normalizedValues = append(normalizedValues, normalized)
	}

	return normalizedValues, nil
}

func computedIPv4AllowedIPs(routing *Routing) ([]string, error) {
	base := routing.AllowedIPs
	if routing.Mode == RoutingModeBypass {
		base = []string{"0.0.0.0/0"}
	}

	remaining := subtractIPv4Ranges(prefixesToIPv4Ranges(base), prefixesToIPv4Ranges(routing.ExcludedIPs))
	computed := ipv4RangesToPrefixes(remaining)
	if len(computed) == 0 {
		return nil, invalidRouting("routing.excluded_ips", "must not exclude the complete IPv4 routing set")
	}
	if len(computed) > MaxComputedRoutingCIDRs {
		return nil, invalidRouting("routing.excluded_ips", fmt.Sprintf("computed routing exceeds %d IPv4 CIDRs", MaxComputedRoutingCIDRs))
	}

	return computed, nil
}

func invalidRouting(field, reason string) error {
	return &RoutingValidationError{
		Field:  field,
		Reason: reason,
	}
}
