package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestReleaseAssetNames(t *testing.T) {
	tests := []struct {
		name       string
		goos       string
		goarch     string
		want       string
		legacyName string
	}{
		{name: "darwin amd64", goos: "darwin", goarch: "amd64", want: "awg-server-awg31-darwin-amd64", legacyName: "awg-server-darwin-amd64"},
		{name: "darwin arm64", goos: "darwin", goarch: "arm64", want: "awg-server-awg31-darwin-arm64", legacyName: "awg-server-darwin-arm64"},
		{name: "linux amd64", goos: "linux", goarch: "amd64", want: "awg-server-awg31-linux-amd64", legacyName: "awg-server-linux-amd64"},
		{name: "linux arm64", goos: "linux", goarch: "arm64", want: "awg-server-awg31-linux-arm64", legacyName: "awg-server-linux-arm64"},
		{name: "windows amd64", goos: "windows", goarch: "amd64", want: "awg-server-awg31-windows-amd64.exe", legacyName: "awg-server-windows-amd64.exe"},
		{name: "windows arm64", goos: "windows", goarch: "arm64", want: "awg-server-awg31-windows-arm64.exe", legacyName: "awg-server-windows-arm64.exe"},
	}

	if len(releaseAssetNames) != len(tests) {
		t.Fatalf("releaseAssetNames has %d entries, want %d", len(releaseAssetNames), len(tests))
	}
	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := releaseAssetName(tt.goos, tt.goarch); got != tt.want {
				t.Fatalf("releaseAssetName(%q, %q) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
			}
			if releaseAssetNames[index] != tt.want {
				t.Fatalf("releaseAssetNames[%d] = %q, want %q", index, releaseAssetNames[index], tt.want)
			}
			for _, name := range releaseAssetNames {
				if name == tt.legacyName {
					t.Fatalf("legacy asset %q overlaps AWG31 asset set", tt.legacyName)
				}
			}
		})
	}
}

func TestSelectReleaseAssetURLsRequiresExactAWG31AssetSet(t *testing.T) {
	version := "1.2.3"
	allNames := append(append([]string(nil), releaseAssetNames...), checksumAssetName, signatureAssetName)
	canonicalAssets := func(names []string) []asset {
		assets := make([]asset, 0, len(names))
		for _, name := range names {
			assets = append(assets, asset{Name: name, BrowserDownloadURL: expectedAssetURL(version, name)})
		}
		return assets
	}

	tests := []struct {
		name   string
		assets []asset
		want   string
	}{
		{
			name:   "exact six plus checksum and signature",
			assets: canonicalAssets(allNames),
		},
		{
			name: "legacy-only binaries",
			assets: canonicalAssets(append(
				append([]string{
					"awg-server-darwin-amd64",
					"awg-server-darwin-arm64",
					"awg-server-linux-amd64",
					"awg-server-linux-arm64",
					"awg-server-windows-amd64.exe",
					"awg-server-windows-arm64.exe",
				}, checksumAssetName), signatureAssetName)),
			want: "unexpected release asset",
		},
		{
			name:   "missing binary",
			assets: canonicalAssets(append(append([]string(nil), releaseAssetNames[:len(releaseAssetNames)-1]...), checksumAssetName, signatureAssetName)),
			want:   "must appear exactly once",
		},
		{
			name: "duplicate binary",
			assets: append(canonicalAssets(allNames), asset{
				Name:               releaseAssetNames[0],
				BrowserDownloadURL: expectedAssetURL(version, releaseAssetNames[0]),
			}),
			want: "exactly once",
		},
		{
			name: "extra expected binary",
			assets: append(canonicalAssets(allNames), asset{
				Name:               "awg-server-awg31-linux-ppc64",
				BrowserDownloadURL: expectedAssetURL(version, "awg-server-awg31-linux-ppc64"),
			}),
			want: "unexpected release asset",
		},
		{
			name: "wrong canonical URL",
			assets: func() []asset {
				assets := canonicalAssets(allNames)
				assets[0].BrowserDownloadURL = "https://example.com/awg-server-awg31-darwin-amd64"
				return assets
			}(),
			want: "unexpected download URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := selectReleaseAssetURLs(tt.assets, version, allNames)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("selectReleaseAssetURLs: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("selectReleaseAssetURLs error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestVerifyReleaseManifestRequiresRenamedAWG31Binaries(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}

	legacyNames := []string{
		"awg-server-darwin-amd64",
		"awg-server-darwin-arm64",
		"awg-server-linux-amd64",
		"awg-server-linux-arm64",
		"awg-server-windows-amd64.exe",
		"awg-server-windows-arm64.exe",
	}
	binary := []byte("synthetic binary")
	manifest := releaseManifestWithNames(legacyNames, legacyNames[2], binary)
	signature := ed25519.Sign(privateKey, manifest)
	if _, err := verifyReleaseManifest(publicKey, manifest, signature, legacyNames[2]); err == nil {
		t.Fatal("legacy signed manifest unexpectedly passed AWG31 validation")
	}
}

func TestValidateUpdatePlatform(t *testing.T) {
	for _, supported := range []string{"linux", "darwin"} {
		if err := validateUpdatePlatform(supported); err != nil {
			t.Fatalf("validateUpdatePlatform(%q): %v", supported, err)
		}
	}
	if err := validateUpdatePlatform("windows"); err == nil || !strings.Contains(err.Error(), "Windows") {
		t.Fatalf("validateUpdatePlatform(%q) error = %v", "windows", err)
	}
}

func TestCompareStableVersions(t *testing.T) {
	tests := []struct {
		left  string
		right string
		want  int
	}{
		{left: "0.0.0", right: "0.0.0", want: 0},
		{left: "1.2.3", right: "1.2.4", want: -1},
		{left: "1.10.0", right: "1.9.99", want: 1},
		{left: "999999999999999999999.0.0", right: "2.0.0", want: 1},
	}

	for _, tt := range tests {
		got, err := compareStableVersions(tt.left, tt.right)
		if err != nil {
			t.Fatalf("compareStableVersions(%q, %q): %v", tt.left, tt.right, err)
		}
		if got != tt.want {
			t.Fatalf("compareStableVersions(%q, %q) = %d, want %d", tt.left, tt.right, got, tt.want)
		}
	}

	for _, invalid := range []string{"", "v1.2.3", "1.2", "01.2.3", "1.2.3-rc.1", "1.2.3.4"} {
		if _, err := compareStableVersions(invalid, "1.2.3"); err == nil {
			t.Fatalf("compareStableVersions(%q, %q) unexpectedly succeeded", invalid, "1.2.3")
		}
	}
}

func TestParseReleasePublicKeyRequiresEd25519(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}

	encoded := encodePublicKey(t, publicKey)
	got, err := parseReleasePublicKey(encoded)
	if err != nil {
		t.Fatalf("parse Ed25519 key: %v", err)
	}
	if !publicKey.Equal(got) {
		t.Fatal("parsed Ed25519 key differs from source key")
	}

	rsaKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	if _, err := parseReleasePublicKey(encodePublicKey(t, &rsaKey.PublicKey)); err == nil {
		t.Fatal("RSA public key unexpectedly accepted")
	}

	for _, invalid := range []string{"", "not-base64", base64.StdEncoding.EncodeToString([]byte("not PEM"))} {
		if _, err := parseReleasePublicKey(invalid); err == nil {
			t.Fatalf("invalid public key %q unexpectedly accepted", invalid)
		}
	}
}

func TestEmbeddedReleasePublicKey(t *testing.T) {
	expected, required := os.LookupEnv("AWG_EXPECTED_RELEASE_PUBLIC_KEY")
	if !required && releasePublicKey == "" {
		t.Skip("release public key is supplied only for release builds")
	}
	if required && releasePublicKey != expected {
		t.Fatalf("embedded release public key does not match AWG_EXPECTED_RELEASE_PUBLIC_KEY")
	}
	if _, err := parseReleasePublicKey(releasePublicKey); err != nil {
		t.Fatalf("parse embedded release public key: %v", err)
	}
}

func TestUpdaterCheckSelectsExactSignedAssets(t *testing.T) {
	assetName := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	version := "1.2.3"
	canonicalURL := func(assetName string) string {
		return "https://github.com/StealthSurf-VPN/awg-server/releases/download/v1.2.3/" + assetName
	}
	assets := make([]asset, 0, len(releaseAssetNames)+2)
	for _, name := range releaseAssetNames {
		assets = append(assets, asset{Name: name, BrowserDownloadURL: canonicalURL(name)})
	}
	assets = append(assets,
		asset{Name: checksumAssetName, BrowserDownloadURL: canonicalURL(checksumAssetName)},
		asset{Name: signatureAssetName, BrowserDownloadURL: canonicalURL(signatureAssetName)},
	)
	rel := release{TagName: "v" + version, Assets: assets}
	body, err := json.Marshal(rel)
	if err != nil {
		t.Fatalf("marshal release: %v", err)
	}

	u := New("1.2.2")
	u.publicKey = testReleasePublicKey(t)
	u.client = responseClient(map[string][]byte{latestReleaseURL: body})

	result, err := u.Check()
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !result.NeedsUpdate || result.Latest != version || result.AssetName != assetName {
		t.Fatalf("unexpected Check result: %+v", result)
	}
	if result.DownloadURL != canonicalURL(assetName) ||
		result.ChecksumsURL != canonicalURL(checksumAssetName) ||
		result.SignatureURL != canonicalURL(signatureAssetName) {
		t.Fatalf("unexpected signed asset URLs: %+v", result)
	}
}

func TestUpdaterCheckWithoutEmbeddedKeyFailsBeforeNetwork(t *testing.T) {
	u := New("1.0.0")
	requestCount := 0
	u.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestCount++
		return nil, fmt.Errorf("unexpected request to %s", request.URL)
	})}

	_, err := u.Check()
	if err == nil || !strings.Contains(err.Error(), "not embedded") {
		t.Fatalf("Check error = %v, want missing embedded key error", err)
	}
	if requestCount != 0 {
		t.Fatalf("Check made %d requests without an embedded key, want 0", requestCount)
	}
}

func TestUpdaterCheckRejectsDowngrade(t *testing.T) {
	rel := release{TagName: "v1.2.2"}
	body, _ := json.Marshal(rel)
	u := New("1.2.3")
	u.publicKey = testReleasePublicKey(t)
	u.client = responseClient(map[string][]byte{latestReleaseURL: body})
	if _, err := u.Check(); err == nil || !strings.Contains(err.Error(), "older") {
		t.Fatalf("Check downgrade error = %v", err)
	}
}

func TestUpdaterApplyVerifiesSignedReleaseBeforeReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test fixture is a POSIX shell executable")
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}
	version := "1.1.0"
	assetName := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	binary := []byte("#!/bin/sh\n[ \"$1\" = version ] || exit 2\nprintf 'awg-server 1.1.0\\n'\n")
	manifest := releaseManifest(assetName, binary)
	signature := ed25519.Sign(privateKey, manifest)
	result := validResult(version, assetName)

	execPath := filepath.Join(t.TempDir(), "awg-server")
	writeExecutable(t, execPath, "1.0.0")
	u := New("1.0.0")
	u.publicKey = encodePublicKey(t, publicKey)
	u.executablePath = func() (string, error) { return execPath, nil }
	u.client = responseClient(map[string][]byte{
		result.DownloadURL:  binary,
		result.ChecksumsURL: manifest,
		result.SignatureURL: signature,
	})

	if err := u.Apply(result); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("read updated executable: %v", err)
	}
	if string(got) != string(binary) {
		t.Fatal("installed executable differs from signed release asset")
	}
}

func TestUpdaterApplyWithoutEmbeddedKeyFailsBeforeDownload(t *testing.T) {
	assetName := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	result := validResult("1.1.0", assetName)
	u := New("1.0.0")
	requestCount := 0
	u.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestCount++
		return nil, fmt.Errorf("unexpected request to %s", request.URL)
	})}

	err := u.Apply(result)
	if err == nil || !strings.Contains(err.Error(), "not embedded") {
		t.Fatalf("Apply error = %v, want missing embedded key error", err)
	}
	if requestCount != 0 {
		t.Fatalf("Apply made %d requests without an embedded key, want 0", requestCount)
	}
}

func TestUpdaterApplyRejectsNewerOnDiskVersionBeforeDownload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("self-update is unsupported on Windows")
	}

	execPath := filepath.Join(t.TempDir(), "awg-server")
	writeExecutable(t, execPath, "1.2.0")
	u := New("1.0.0")
	u.publicKey = testReleasePublicKey(t)
	u.executablePath = func() (string, error) { return execPath, nil }
	requestCount := 0
	u.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestCount++
		return nil, fmt.Errorf("unexpected request to %s", request.URL)
	})}

	err := u.Apply(validResult("1.1.0", releaseAssetName(runtime.GOOS, runtime.GOARCH)))
	if err == nil || !strings.Contains(err.Error(), "older than installed version 1.2.0") {
		t.Fatalf("Apply error = %v, want on-disk downgrade rejection", err)
	}
	if requestCount != 0 {
		t.Fatalf("on-disk downgrade made %d requests, want 0", requestCount)
	}
}

func TestUpdaterApplyHonorsInterprocessLockBeforeDownload(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("interprocess update lock is supported on Linux and macOS")
	}

	execPath := filepath.Join(t.TempDir(), "awg-server")
	writeExecutable(t, execPath, "1.0.0")
	lock, err := acquireUpdateLock(execPath)
	if err != nil {
		t.Fatalf("acquire first update lock: %v", err)
	}
	defer lock.Close()

	u := New("1.0.0")
	u.publicKey = testReleasePublicKey(t)
	u.executablePath = func() (string, error) { return execPath, nil }
	requestCount := 0
	u.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requestCount++
		return nil, fmt.Errorf("unexpected request to %s", request.URL)
	})}

	err = u.Apply(validResult("1.1.0", releaseAssetName(runtime.GOOS, runtime.GOARCH)))
	if err == nil || err.Error() != "another update is already in progress" {
		t.Fatalf("Apply error = %v, want update-only interprocess lock rejection", err)
	}
	if requestCount != 0 {
		t.Fatalf("locked update made %d requests, want 0", requestCount)
	}
}

func TestUpdaterApplyRejectsTamperingBeforeReplacement(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test fixture is a POSIX shell executable")
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate Ed25519 key: %v", err)
	}
	version := "1.1.0"
	assetName := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	validBinary := []byte("#!/bin/sh\n[ \"$1\" = version ] || exit 2\nprintf 'awg-server 1.1.0\\n'\n")
	manifest := releaseManifest(assetName, validBinary)
	validSignature := ed25519.Sign(privateKey, manifest)
	manifestLines := strings.Split(strings.TrimSuffix(string(manifest), "\n"), "\n")
	manifestLines[0], manifestLines[1] = manifestLines[1], manifestLines[0]
	nonCanonicalManifest := []byte(strings.Join(manifestLines, "\n") + "\n")
	nonCanonicalSignature := ed25519.Sign(privateKey, nonCanonicalManifest)
	unexpectedURLResult := validResult(version, assetName)
	unexpectedURLResult.DownloadURL = "https://example.com/awg-server"

	tests := []struct {
		name      string
		current   string
		result    *CheckResult
		binary    []byte
		manifest  []byte
		signature []byte
		wantError string
	}{
		{
			name:      "downgrade before download",
			current:   "2.0.0",
			result:    validResult(version, assetName),
			binary:    validBinary,
			manifest:  manifest,
			signature: validSignature,
			wantError: "older",
		},
		{
			name:      "invalid signature",
			current:   "1.0.0",
			result:    validResult(version, assetName),
			binary:    validBinary,
			manifest:  manifest,
			signature: append([]byte(nil), validSignature...),
			wantError: "signature",
		},
		{
			name:      "binary checksum mismatch",
			current:   "1.0.0",
			result:    validResult(version, assetName),
			binary:    append(append([]byte(nil), validBinary...), []byte("tampered")...),
			manifest:  manifest,
			signature: validSignature,
			wantError: "checksum",
		},
		{
			name:      "signed non-canonical manifest",
			current:   "1.0.0",
			result:    validResult(version, assetName),
			binary:    validBinary,
			manifest:  nonCanonicalManifest,
			signature: nonCanonicalSignature,
			wantError: "unexpected asset set",
		},
		{
			name:      "embedded version mismatch",
			current:   "1.0.0",
			result:    validResult("1.1.1", assetName),
			binary:    validBinary,
			manifest:  manifest,
			signature: validSignature,
			wantError: "reports",
		},
		{
			name:      "unexpected version-bound URL",
			current:   "1.0.0",
			result:    unexpectedURLResult,
			binary:    validBinary,
			manifest:  manifest,
			signature: validSignature,
			wantError: "unexpected download URL",
		},
	}
	tests[1].signature[0] ^= 0xff

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			execPath := filepath.Join(t.TempDir(), "awg-server")
			writeExecutable(t, execPath, tt.current)
			before, err := os.ReadFile(execPath)
			if err != nil {
				t.Fatalf("read original executable: %v", err)
			}

			u := New(tt.current)
			u.publicKey = encodePublicKey(t, publicKey)
			u.executablePath = func() (string, error) { return execPath, nil }
			requestCount := 0
			u.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				requestCount++
				responses := map[string][]byte{
					tt.result.DownloadURL:  tt.binary,
					tt.result.ChecksumsURL: tt.manifest,
					tt.result.SignatureURL: tt.signature,
				}
				body, ok := responses[request.URL.String()]
				if !ok {
					return nil, fmt.Errorf("unexpected URL %s", request.URL)
				}
				return okResponse(body), nil
			})}

			err = u.Apply(tt.result)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("Apply error = %v, want substring %q", err, tt.wantError)
			}
			if (tt.name == "downgrade before download" || tt.name == "unexpected version-bound URL") && requestCount != 0 {
				t.Fatalf("pre-download rejection made %d requests, want 0", requestCount)
			}

			after, err := os.ReadFile(execPath)
			if err != nil {
				t.Fatalf("read executable after failure: %v", err)
			}
			if string(after) != string(before) {
				t.Fatal("failed update changed the installed executable")
			}
		})
	}
}

func encodePublicKey(t *testing.T, key any) string {
	t.Helper()

	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return base64.StdEncoding.EncodeToString(pemBytes)
}

func testReleasePublicKey(t *testing.T) string {
	t.Helper()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate test release key: %v", err)
	}
	return encodePublicKey(t, publicKey)
}

func releaseManifest(selectedAsset string, binary []byte) []byte {
	return releaseManifestWithNames(releaseAssetNames, selectedAsset, binary)
}

func releaseManifestWithNames(assetNames []string, selectedAsset string, binary []byte) []byte {
	digest := sha256.Sum256(binary)
	var builder strings.Builder
	for _, assetName := range assetNames {
		assetDigest := strings.Repeat("0", sha256.Size*2)
		if assetName == selectedAsset {
			assetDigest = hex.EncodeToString(digest[:])
		}
		fmt.Fprintf(&builder, "%s  %s\n", assetDigest, assetName)
	}
	return []byte(builder.String())
}

func validResult(version, assetName string) *CheckResult {
	return &CheckResult{
		Latest:       version,
		AssetName:    assetName,
		DownloadURL:  expectedAssetURL(version, assetName),
		ChecksumsURL: expectedAssetURL(version, checksumAssetName),
		SignatureURL: expectedAssetURL(version, signatureAssetName),
		NeedsUpdate:  true,
	}
}

func responseClient(responses map[string][]byte) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, ok := responses[request.URL.String()]
		if !ok {
			return nil, fmt.Errorf("unexpected URL %s", request.URL)
		}
		return okResponse(body), nil
	})}
}

func okResponse(body []byte) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(body))),
	}
}

func writeExecutable(t *testing.T, path, version string) {
	t.Helper()
	contents := fmt.Sprintf("#!/bin/sh\n[ \"$1\" = version ] || exit 2\nprintf 'awg-server %s\\n'\n", version)
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}
}
