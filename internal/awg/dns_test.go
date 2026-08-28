package awg

import "testing"

func TestNormalizeOverridesForVersionClonesRangesAndDNSServers(t *testing.T) {
	params := &AWGParams{
		DNSMode:                DNSModeCustom,
		DNSServers:             []string{"1.1.1.1", "1.0.0.1"},
		PersistentKeepalive:    rangePointer(t, "25-35"),
		ContentPaddingAddition: rangePointer(t, "10-100"),
		RekeyAfterTime:         rangePointer(t, "100-120"),
		RekeyTimeout:           rangePointer(t, "3-7"),
		RejectAfterTime:        rangePointer(t, "150-180"),
		KeepaliveTimeout:       rangePointer(t, "5-15"),
		MaxHandshakeAttempts:   rangePointer(t, "15-20"),
		RandomTrailers:         "on",
		DisableCookies:         "off",
	}

	normalized, err := NormalizeOverridesForVersion(ProtocolVersion31, params)
	if err != nil {
		t.Fatalf("NormalizeOverridesForVersion() error = %v", err)
	}
	if normalized == params {
		t.Fatal("NormalizeOverridesForVersion() returned the input pointer")
	}
	if &normalized.DNSServers[0] == &params.DNSServers[0] {
		t.Fatal("NormalizeOverridesForVersion() did not clone DNS servers")
	}

	ranges := []struct {
		name string
		got  any
		want any
	}{
		{name: "persistent keepalive", got: normalized.PersistentKeepalive, want: params.PersistentKeepalive},
		{name: "content padding", got: normalized.ContentPaddingAddition, want: params.ContentPaddingAddition},
		{name: "rekey after", got: normalized.RekeyAfterTime, want: params.RekeyAfterTime},
		{name: "rekey timeout", got: normalized.RekeyTimeout, want: params.RekeyTimeout},
		{name: "reject after", got: normalized.RejectAfterTime, want: params.RejectAfterTime},
		{name: "keepalive timeout", got: normalized.KeepaliveTimeout, want: params.KeepaliveTimeout},
		{name: "max handshake attempts", got: normalized.MaxHandshakeAttempts, want: params.MaxHandshakeAttempts},
	}

	for _, tt := range ranges {
		if tt.got == tt.want {
			t.Fatalf("NormalizeOverridesForVersion() did not clone %s", tt.name)
		}
	}
}
