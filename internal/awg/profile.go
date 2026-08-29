package awg

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"

	"github.com/stealthsurf-vpn/awg-server/internal/config"
)

const profileHashDomain = "awg-server/profile/v1"

type HeaderProtectionKey [32]byte

type ProfileKey [32]byte

var errProfileKeyJSONSerialization = errors.New("profile key JSON serialization is disabled")

func (HeaderProtectionKey) MarshalJSON() ([]byte, error) {
	return nil, errProfileKeyJSONSerialization
}

func (ProfileKey) MarshalJSON() ([]byte, error) {
	return nil, errProfileKeyJSONSerialization
}

type Profile struct {
	version                ProtocolVersion
	params                 AWGParams
	headerProtectionKey    HeaderProtectionKey
	hasHeaderProtectionKey bool
	key                    ProfileKey
	validated              bool
}

func NewLegacyProfile(params AWGParams) (Profile, error) {
	if err := ValidateProfileForVersion(ProtocolVersion2, params); err != nil {
		return Profile{}, fmt.Errorf("validate legacy profile: %w", err)
	}

	return newProfile(ProtocolVersion2, params, HeaderProtectionKey{}, false), nil
}

func NewAWG31Profile(params AWGParams, headerProtectionKey HeaderProtectionKey) (Profile, error) {
	if headerProtectionKey == (HeaderProtectionKey{}) {
		return Profile{}, errors.New("header protection key must not be all zero")
	}

	if err := ValidateProfileForVersion(ProtocolVersion31, params); err != nil {
		return Profile{}, fmt.Errorf("validate AWG 3.1 profile: %w", err)
	}

	return newProfile(ProtocolVersion31, params, headerProtectionKey, true), nil
}

func GenerateHeaderProtectionKey() (HeaderProtectionKey, error) {
	for {
		var key HeaderProtectionKey

		if _, err := rand.Read(key[:]); err != nil {
			return HeaderProtectionKey{}, fmt.Errorf("generate header protection key: %w", err)
		}
		if key != (HeaderProtectionKey{}) {
			return key, nil
		}
	}
}

func HeaderProtectionKeyToBase64(key HeaderProtectionKey) string {
	return base64.StdEncoding.EncodeToString(key[:])
}

func Base64ToHeaderProtectionKey(encoded string) (HeaderProtectionKey, error) {
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return HeaderProtectionKey{}, fmt.Errorf("decode header protection key: %w", err)
	}
	if len(decoded) != len(HeaderProtectionKey{}) {
		return HeaderProtectionKey{}, errors.New("invalid header protection key length")
	}
	if base64.StdEncoding.EncodeToString(decoded) != encoded {
		return HeaderProtectionKey{}, errors.New("invalid header protection key encoding")
	}

	var key HeaderProtectionKey
	copy(key[:], decoded)
	if key == (HeaderProtectionKey{}) {
		return HeaderProtectionKey{}, errors.New("header protection key must not be all zero")
	}

	return key, nil
}

func newProfile(version ProtocolVersion, params AWGParams, headerProtectionKey HeaderProtectionKey, hasHeaderProtectionKey bool) Profile {
	clonedParams := cloneAWGParams(&params)
	canonicalizeUnsignedRanges(clonedParams)

	profile := Profile{
		version:                version,
		params:                 *clonedParams,
		headerProtectionKey:    headerProtectionKey,
		hasHeaderProtectionKey: hasHeaderProtectionKey,
		validated:              true,
	}
	profile.key = canonicalProfileKey(profile)

	return profile
}

func (profile Profile) Version() ProtocolVersion {
	return profile.version
}

func (profile Profile) Params() AWGParams {
	return *cloneAWGParams(&profile.params)
}

func (profile Profile) Key() ProfileKey {
	return profile.key
}

func (profile Profile) ClientConfigLines() string {
	lines := profile.params.ConfigLines()
	if profile.hasHeaderProtectionKey {
		lines += "\nHeaderProtectionKey = " + HeaderProtectionKeyToBase64(profile.headerProtectionKey)
	}

	return lines
}

func (profile Profile) ServerConfig(privateKey [32]byte, port int) string {
	config := "[Interface]\nPrivateKey = " + KeyToBase64(privateKey) + fmt.Sprintf("\nListenPort = %d", port)
	config += profile.params.serverConfigLines()
	if profile.hasHeaderProtectionKey {
		config += "\nHeaderProtectionKey = " + HeaderProtectionKeyToBase64(profile.headerProtectionKey)
	}

	return config + "\n"
}

func (profile Profile) valid() bool {
	return profile.validated && profile.version.Valid()
}

func canonicalProfileKey(profile Profile) ProfileKey {
	digest := sha256.New()

	writeProfileBytes(digest, []byte(profileHashDomain))
	writeProfileString(digest, profile.version.String())
	writeProfileInt(digest, profile.params.Jc)
	writeProfileInt(digest, profile.params.Jmin)
	writeProfileInt(digest, profile.params.Jmax)
	writeProfileInt(digest, profile.params.S1)
	writeProfileInt(digest, profile.params.S2)
	writeProfileInt(digest, profile.params.S3)
	writeProfileInt(digest, profile.params.S4)
	writeProfileHeaderRange(digest, profile.params.H1)
	writeProfileHeaderRange(digest, profile.params.H2)
	writeProfileHeaderRange(digest, profile.params.H3)
	writeProfileHeaderRange(digest, profile.params.H4)
	writeProfileOptionalRange(digest, profile.params.ContentPaddingAddition)
	writeProfileOptionalRange(digest, profile.params.RekeyAfterTime)
	writeProfileOptionalRange(digest, profile.params.RekeyTimeout)
	writeProfileOptionalRange(digest, profile.params.RejectAfterTime)
	writeProfileOptionalRange(digest, profile.params.KeepaliveTimeout)
	writeProfileOptionalRange(digest, profile.params.MaxHandshakeAttempts)
	writeProfileString(digest, profile.params.RandomTrailers)
	writeProfileString(digest, profile.params.DisableCookies)

	if profile.hasHeaderProtectionKey {
		writeProfileBytes(digest, profile.headerProtectionKey[:])
	} else {
		writeProfileBytes(digest, nil)
	}

	var key ProfileKey
	copy(key[:], digest.Sum(nil))

	return key
}

func writeProfileBytes(digest hash.Hash, value []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	_, _ = digest.Write(length[:])
	_, _ = digest.Write(value)
}

func writeProfileString(digest hash.Hash, value string) {
	writeProfileBytes(digest, []byte(value))
}

func writeProfileInt(digest hash.Hash, value int) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))
	_, _ = digest.Write(encoded[:])
}

func writeProfileHeaderRange(digest hash.Hash, value string) {
	rangeValue, _ := parseHeaderRange("header", value)

	var encoded [8]byte
	binary.BigEndian.PutUint32(encoded[:4], rangeValue.start)
	binary.BigEndian.PutUint32(encoded[4:], rangeValue.end)
	_, _ = digest.Write(encoded[:])
}

func writeProfileOptionalRange(digest hash.Hash, value *config.Uint16Range) {
	if value == nil {
		writeProfileBytes(digest, nil)
		return
	}

	writeProfileString(digest, value.String())
}
