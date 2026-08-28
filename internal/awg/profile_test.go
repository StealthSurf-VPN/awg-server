package awg

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestLegacyProfileRejectsAWG31State(t *testing.T) {
	params := validLegacyProfileParams()
	params.ContentPaddingAddition = rangePointer(t, "10-100")

	_, err := NewLegacyProfile(params)
	if !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("NewLegacyProfile() error = %v, want invalid params", err)
	}

	profile, err := NewLegacyProfile(validLegacyProfileParams())
	if err != nil {
		t.Fatalf("NewLegacyProfile() error = %v", err)
	}

	if strings.Contains(profile.ClientConfigLines(), "HeaderProtectionKey") {
		t.Fatalf("legacy ClientConfigLines() contains header protection key: %q", profile.ClientConfigLines())
	}
	if strings.Contains(profile.ServerConfig([32]byte{1}, 51820), "HeaderProtectionKey") {
		t.Fatal("legacy ServerConfig() contains header protection key")
	}
}

func TestAWG31ProfileRequiresNonZeroKeyAndPadding(t *testing.T) {
	params := validAWG31ProfileParams(t)

	if _, err := NewAWG31Profile(params, HeaderProtectionKey{}); err == nil {
		t.Fatal("NewAWG31Profile() accepted an all-zero header protection key")
	}

	params.S4 = 11
	if _, err := NewAWG31Profile(params, syntheticHeaderProtectionKey()); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("NewAWG31Profile() error = %v, want invalid params", err)
	}
}

func TestProfileKeyIncludesOnlyServerIdentity(t *testing.T) {
	base := validAWG31ProfileParams(t)
	headerKey := syntheticHeaderProtectionKey()

	profile, err := NewAWG31Profile(base, headerKey)
	if err != nil {
		t.Fatalf("NewAWG31Profile() error = %v", err)
	}

	changes := []struct {
		name   string
		mutate func(*AWGParams, *HeaderProtectionKey)
	}{
		{name: "header protection key", mutate: func(_ *AWGParams, key *HeaderProtectionKey) { key[0]++ }},
		{name: "jc", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.Jc++ }},
		{name: "jmin", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.Jmin++ }},
		{name: "jmax", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.Jmax++ }},
		{name: "s1", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.S1 = 17 }},
		{name: "s2", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.S2 = 73 }},
		{name: "s3", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.S3++ }},
		{name: "s4", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.S4++ }},
		{name: "h1", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.H1 = "2" }},
		{name: "h2", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.H2 = "4" }},
		{name: "h3", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.H3 = "6" }},
		{name: "h4", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.H4 = "8" }},
		{name: "content padding addition", mutate: func(params *AWGParams, _ *HeaderProtectionKey) {
			params.ContentPaddingAddition = rangePointer(t, "11-100")
		}},
		{name: "rekey after time", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.RekeyAfterTime = rangePointer(t, "101-120") }},
		{name: "rekey timeout", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.RekeyTimeout = rangePointer(t, "4-7") }},
		{name: "reject after time", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.RejectAfterTime = rangePointer(t, "151-180") }},
		{name: "keepalive timeout", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.KeepaliveTimeout = rangePointer(t, "6-15") }},
		{name: "max handshake attempts", mutate: func(params *AWGParams, _ *HeaderProtectionKey) {
			params.MaxHandshakeAttempts = rangePointer(t, "16-20")
		}},
		{name: "random trailers", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.RandomTrailers = "off" }},
		{name: "disable cookies", mutate: func(params *AWGParams, _ *HeaderProtectionKey) { params.DisableCookies = "on" }},
	}

	for _, tt := range changes {
		t.Run(tt.name, func(t *testing.T) {
			params := *cloneAWGParams(&base)
			key := headerKey
			tt.mutate(&params, &key)

			changed, err := NewAWG31Profile(params, key)
			if err != nil {
				t.Fatalf("NewAWG31Profile() error = %v", err)
			}
			if changed.Key() == profile.Key() {
				t.Fatal("ProfileKey did not change for a server-applied value")
			}
		})
	}

	legacyParams := validLegacyProfileParams()
	legacyParams.S3 = base.S3
	legacyParams.S4 = base.S4
	legacy, err := NewLegacyProfile(legacyParams)
	if err != nil {
		t.Fatalf("NewLegacyProfile() error = %v", err)
	}
	if legacy.Key() == profile.Key() {
		t.Fatal("ProfileKey did not change for protocol version")
	}

	clientOnly := *cloneAWGParams(&base)
	clientOnly.Port = 51830
	clientOnly.ClientListenPort = 51831
	clientOnly.MTU = 1380
	clientOnly.DNSMode = DNSModeCustom
	clientOnly.DNSServers = []string{"9.9.9.9"}
	clientOnly.PersistentKeepalive = rangePointer(t, "30-40")
	clientOnly.I1 = "<t>"
	clientOnly.I2 = "<r 10>"
	clientOnly.I3 = "<rc 10>"
	clientOnly.I4 = "<rd 10>"
	clientOnly.I5 = "<b 0xff>"

	unchanged, err := NewAWG31Profile(clientOnly, headerKey)
	if err != nil {
		t.Fatalf("NewAWG31Profile() error = %v", err)
	}
	if unchanged.Key() != profile.Key() {
		t.Fatal("ProfileKey changed for a client-only value")
	}
}

func TestProfileKeyCanonicalizesHeaderRanges(t *testing.T) {
	params := validAWG31ProfileParams(t)
	profile, err := NewAWG31Profile(params, syntheticHeaderProtectionKey())
	if err != nil {
		t.Fatalf("NewAWG31Profile() error = %v", err)
	}

	canonicalized := *cloneAWGParams(&params)
	canonicalized.H1 = "001"
	canonicalized.H2 = "0003-0003"
	canonicalized.H3 = "0005"
	canonicalized.H4 = "0007-0007"

	other, err := NewAWG31Profile(canonicalized, syntheticHeaderProtectionKey())
	if err != nil {
		t.Fatalf("NewAWG31Profile() error = %v", err)
	}
	if other.Key() != profile.Key() {
		t.Fatal("ProfileKey changed for equivalent header range values")
	}
}

func TestProfileCopiesParams(t *testing.T) {
	params := validAWG31ProfileParams(t)
	params.DNSMode = DNSModeCustom
	params.DNSServers = []string{"1.1.1.1"}

	profile, err := NewAWG31Profile(params, syntheticHeaderProtectionKey())
	if err != nil {
		t.Fatalf("NewAWG31Profile() error = %v", err)
	}

	params.H1 = "99"
	params.DNSServers[0] = "9.9.9.9"
	params.PersistentKeepalive = rangePointer(t, "off")

	first := profile.Params()
	if first.H1 != "1" || first.DNSServers[0] != "1.1.1.1" || first.PersistentKeepalive.String() != "25-35" {
		t.Fatalf("Profile.Params() = %+v, want independent original values", first)
	}

	first.H1 = "100"
	first.DNSServers[0] = "8.8.8.8"
	first.PersistentKeepalive = rangePointer(t, "off")

	second := profile.Params()
	if second.H1 != "1" || second.DNSServers[0] != "1.1.1.1" || second.PersistentKeepalive.String() != "25-35" {
		t.Fatalf("Profile.Params() returned aliased values: %+v", second)
	}
}

func TestHeaderProtectionKeyEncodingAndErrorsDoNotLeak(t *testing.T) {
	key := syntheticHeaderProtectionKey()

	encoded := HeaderProtectionKeyToBase64(key)
	decoded, err := Base64ToHeaderProtectionKey(encoded)
	if err != nil {
		t.Fatalf("Base64ToHeaderProtectionKey() error = %v", err)
	}
	if decoded != key {
		t.Fatal("Base64ToHeaderProtectionKey() did not round-trip the key")
	}

	for _, input := range []string{"not base64", strings.TrimRight(encoded, "="), KeyToBase64([32]byte{})} {
		if _, err := Base64ToHeaderProtectionKey(input); err == nil {
			t.Fatalf("Base64ToHeaderProtectionKey(%q) succeeded", input)
		}
	}

	generated, err := GenerateHeaderProtectionKey()
	if err != nil {
		t.Fatalf("GenerateHeaderProtectionKey() error = %v", err)
	}
	if generated == (HeaderProtectionKey{}) {
		t.Fatal("GenerateHeaderProtectionKey() returned an all-zero key")
	}

	params := validAWG31ProfileParams(t)
	params.S4 = 0
	_, err = NewAWG31Profile(params, key)
	if err == nil {
		t.Fatal("NewAWG31Profile() accepted invalid params")
	}
	message := fmt.Sprint(err)
	if strings.Contains(message, encoded) || strings.Contains(message, fmt.Sprintf("%x", key)) {
		t.Fatalf("profile error leaks header protection key: %q", message)
	}

	stringer := reflect.TypeOf((*fmt.Stringer)(nil)).Elem()
	if reflect.TypeOf(HeaderProtectionKey{}).Implements(stringer) || reflect.TypeOf(ProfileKey{}).Implements(stringer) {
		t.Fatal("secret or profile identity type implements fmt.Stringer")
	}
}

func TestProfileConfigurationSeparatesServerAndClientFields(t *testing.T) {
	params := validAWG31ProfileParams(t)
	params.MTU = 1380
	params.DNSMode = DNSModeCustom
	params.DNSServers = []string{"1.1.1.1"}
	params.ClientListenPort = 51821
	params.PersistentKeepalive = rangePointer(t, "25-35")
	params.I1 = "<t>"
	params.I2 = "<r 10>"

	profile, err := NewAWG31Profile(params, syntheticHeaderProtectionKey())
	if err != nil {
		t.Fatalf("NewAWG31Profile() error = %v", err)
	}

	privateKey := [32]byte{9}
	serverConfig := profile.ServerConfig(privateKey, 51820)
	for _, line := range []string{
		"[Interface]",
		"PrivateKey = " + KeyToBase64(privateKey),
		"ListenPort = 51820",
		"Jc = 5",
		"S1 = 15",
		"H1 = 1",
		"ContentPaddingAddition = 10-100",
		"RandomTrailers = on",
		"HeaderProtectionKey = " + HeaderProtectionKeyToBase64(syntheticHeaderProtectionKey()),
	} {
		if !strings.Contains(serverConfig, line) {
			t.Fatalf("ServerConfig() missing %q:\n%s", line, serverConfig)
		}
	}
	for _, line := range []string{"I1 =", "I2 =", "MTU =", "DNS =", "ClientListenPort", "PersistentKeepalive"} {
		if strings.Contains(serverConfig, line) {
			t.Fatalf("ServerConfig() contains client-only field %q:\n%s", line, serverConfig)
		}
	}

	clientLines := profile.ClientConfigLines()
	for _, line := range []string{
		"I1 = <t>",
		"I2 = <r 10>",
		"ContentPaddingAddition = 10-100",
		"HeaderProtectionKey = " + HeaderProtectionKeyToBase64(syntheticHeaderProtectionKey()),
	} {
		if !strings.Contains(clientLines, line) {
			t.Fatalf("ClientConfigLines() missing %q:\n%s", line, clientLines)
		}
	}
}

func validLegacyProfileParams() AWGParams {
	return AWGParams{
		Jc: 5, Jmin: 50, Jmax: 1000,
		S1: 15, S2: 72,
		H1: "1-2", H2: "3-4", H3: "5-6", H4: "7-8",
	}
}

func validAWG31ProfileParams(t *testing.T) AWGParams {
	t.Helper()

	return AWGParams{
		Jc: 5, Jmin: 50, Jmax: 1000,
		S1: 15, S2: 72, S3: 15, S4: 12,
		H1: "1", H2: "3", H3: "5", H4: "7",
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
}

func syntheticHeaderProtectionKey() HeaderProtectionKey {
	return HeaderProtectionKey{
		1, 2, 3, 4, 5, 6, 7, 8,
		9, 10, 11, 12, 13, 14, 15, 16,
		17, 18, 19, 20, 21, 22, 23, 24,
		25, 26, 27, 28, 29, 30, 31, 32,
	}
}
