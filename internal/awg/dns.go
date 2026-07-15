package awg

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
)

const (
	DNSModeDefault = "default"
	DNSModeCustom  = "custom"
	DNSModeSystem  = "system"
)

var ErrInvalidDNS = errors.New("dns must be empty or a valid IPv4 address")

func (params *AWGParams) UnmarshalJSON(data []byte) error {
	type plainAWGParams AWGParams

	var decoded plainAWGParams
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}

	*params = AWGParams(decoded)

	for field := range fields {
		switch {
		case strings.EqualFold(field, "dns"):
			params.dnsSet = true
		case strings.EqualFold(field, "dns_mode"):
			params.dnsModeSet = true
		case strings.EqualFold(field, "dns_servers"):
			params.dnsServersSet = true
		}
	}

	return nil
}

func ValidateDNS(dns string) error {
	if dns == "" {
		return nil
	}

	ip, err := netip.ParseAddr(dns)
	if err != nil || !ip.Is4() {
		return ErrInvalidDNS
	}

	return nil
}

func cloneAWGParams(params *AWGParams) *AWGParams {
	if params == nil {
		return nil
	}

	clone := *params
	if params.DNSServers != nil {
		clone.DNSServers = make([]string, len(params.DNSServers))
		copy(clone.DNSServers, params.DNSServers)
	}

	if params.PersistentKeepalive != nil {
		keepalive := *params.PersistentKeepalive
		clone.PersistentKeepalive = &keepalive
	}

	return &clone
}

func validateDNSSettings(params *AWGParams) error {
	legacySet := params.dnsSet || params.DNS != ""
	modeSet := params.dnsModeSet || params.DNSMode != ""
	serversSet := params.dnsServersSet || params.DNSServers != nil

	if legacySet && (modeSet || serversSet) {
		return invalidParam("dns", "cannot be combined with dns_mode or dns_servers")
	}

	if !modeSet {
		if serversSet {
			return invalidParam("dns_servers", "requires dns_mode")
		}

		if err := ValidateDNS(params.DNS); err != nil {
			return validationFrom("dns", err)
		}

		return nil
	}

	switch params.DNSMode {
	case DNSModeDefault, DNSModeSystem:
		if len(params.DNSServers) > 0 {
			return invalidParam("dns_servers", "must be empty for dns_mode %q", params.DNSMode)
		}
	case DNSModeCustom:
		if len(params.DNSServers) == 0 {
			return invalidParam("dns_servers", "must contain at least one IPv4 address for custom mode")
		}
	default:
		return invalidParam("dns_mode", "must be default, custom, or system")
	}

	for i, value := range params.DNSServers {
		address, err := netip.ParseAddr(value)
		if err != nil || !address.Is4() {
			return invalidParam(fmt.Sprintf("dns_servers[%d]", i), "must be a valid IPv4 address")
		}
	}

	return nil
}

func NormalizeOverrides(params *AWGParams) (*AWGParams, error) {
	if params == nil {
		return nil, nil
	}

	normalized := cloneAWGParams(params)
	if err := validateDNSSettings(normalized); err != nil {
		return nil, err
	}

	if normalized.DNSMode == DNSModeCustom {
		seen := make(map[string]bool, len(normalized.DNSServers))
		servers := make([]string, 0, len(normalized.DNSServers))

		for _, value := range normalized.DNSServers {
			address, _ := netip.ParseAddr(value)
			canonical := address.String()

			if !seen[canonical] {
				seen[canonical] = true
				servers = append(servers, canonical)
			}
		}

		normalized.DNSServers = servers
	}

	if err := ValidateOverrides(normalized); err != nil {
		return nil, err
	}

	normalized.dnsSet = false
	normalized.dnsModeSet = false
	normalized.dnsServersSet = false

	if reflect.DeepEqual(*normalized, AWGParams{}) {
		return nil, nil
	}

	return normalized, nil
}

func ResolveDNS(params *AWGParams, defaultDNS string) (string, bool) {
	if params == nil {
		return defaultDNS, true
	}

	switch params.DNSMode {
	case DNSModeDefault:
		return defaultDNS, true
	case DNSModeCustom:
		return strings.Join(params.DNSServers, ", "), true
	case DNSModeSystem:
		return "", false
	default:
		if params.DNS != "" {
			return params.DNS, true
		}

		return defaultDNS, true
	}
}
