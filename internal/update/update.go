package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	repo               = "StealthSurf-VPN/awg-server"
	binaryName         = "awg-server"
	latestReleaseURL   = "https://api.github.com/repos/" + repo + "/releases/latest"
	checksumAssetName  = "SHA256SUMS"
	signatureAssetName = "SHA256SUMS.sig"
	maxReleaseMetadata = 64 << 10
	maxReleaseBinary   = 64 << 20
)

var (
	releasePublicKey  string
	releaseAssetNames = []string{
		"awg-server-darwin-amd64",
		"awg-server-darwin-arm64",
		"awg-server-linux-amd64",
		"awg-server-linux-arm64",
		"awg-server-windows-amd64.exe",
		"awg-server-windows-arm64.exe",
	}
)

type Updater struct {
	current        string
	client         *http.Client
	publicKey      string
	executablePath func() (string, error)
}

type CheckResult struct {
	Latest       string
	AssetName    string
	DownloadURL  string
	ChecksumsURL string
	SignatureURL string
	NeedsUpdate  bool
}

type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func New(currentVersion string) *Updater {
	return &Updater{
		current:        currentVersion,
		client:         &http.Client{Timeout: 30 * time.Second},
		publicKey:      releasePublicKey,
		executablePath: os.Executable,
	}
}

func (u *Updater) Check() (*CheckResult, error) {
	if err := validateUpdatePlatform(runtime.GOOS); err != nil {
		return nil, err
	}
	if _, err := parseReleasePublicKey(u.publicKey); err != nil {
		return nil, err
	}

	resp, err := u.client.Get(latestReleaseURL)
	if err != nil {
		return nil, fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&rel); err != nil {
		return nil, fmt.Errorf("decode release: %w", err)
	}

	if !strings.HasPrefix(rel.TagName, "v") {
		return nil, fmt.Errorf("latest release tag %q is not a stable vMAJOR.MINOR.PATCH tag", rel.TagName)
	}
	latest := strings.TrimPrefix(rel.TagName, "v")
	if _, err := parseStableVersion(latest); err != nil {
		return nil, fmt.Errorf("latest release tag %q: %w", rel.TagName, err)
	}

	comparison, err := compareCurrentVersion(u.current, latest)
	if err != nil {
		return nil, err
	}
	if comparison > 0 {
		return nil, fmt.Errorf("latest release %s is older than installed version %s", latest, u.current)
	}
	if comparison == 0 {
		return &CheckResult{Latest: latest}, nil
	}

	assetName := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	urls, err := selectReleaseAssetURLs(rel.Assets, latest, []string{
		assetName,
		checksumAssetName,
		signatureAssetName,
	})
	if err != nil {
		return nil, err
	}

	return &CheckResult{
		Latest:       latest,
		AssetName:    assetName,
		DownloadURL:  urls[assetName],
		ChecksumsURL: urls[checksumAssetName],
		SignatureURL: urls[signatureAssetName],
		NeedsUpdate:  true,
	}, nil
}

func validateUpdatePlatform(goos string) error {
	switch goos {
	case "linux", "darwin":
		return nil
	case "windows":
		return errors.New("self-update is unavailable on Windows because a running executable cannot be replaced atomically; install a separately verified signed release")
	default:
		return fmt.Errorf("self-update is unsupported on %s", goos)
	}
}

func releaseAssetName(goos, goarch string) string {
	name := fmt.Sprintf("%s-%s-%s", binaryName, goos, goarch)
	if goos == "windows" {
		name += ".exe"
	}

	return name
}

func expectedAssetURL(version, assetName string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", repo, version, assetName)
}

func selectReleaseAssetURLs(assets []asset, version string, names []string) (map[string]string, error) {
	requested := make(map[string]struct{}, len(names))
	for _, name := range names {
		requested[name] = struct{}{}
	}

	urls := make(map[string]string, len(names))
	counts := make(map[string]int, len(names))
	for _, releaseAsset := range assets {
		if _, ok := requested[releaseAsset.Name]; !ok {
			continue
		}

		counts[releaseAsset.Name]++
		urls[releaseAsset.Name] = releaseAsset.BrowserDownloadURL
	}

	for _, name := range names {
		if counts[name] != 1 {
			return nil, fmt.Errorf("release asset %s must appear exactly once", name)
		}
		expectedURL := expectedAssetURL(version, name)
		if urls[name] != expectedURL {
			return nil, fmt.Errorf("release asset %s has unexpected download URL %q", name, urls[name])
		}
	}

	return urls, nil
}

func compareCurrentVersion(current, latest string) (int, error) {
	if current == "dev" {
		return -1, nil
	}

	current = strings.TrimPrefix(current, "v")
	comparison, err := compareStableVersions(current, latest)
	if err != nil {
		return 0, fmt.Errorf("compare installed version %q with latest %q: %w", current, latest, err)
	}
	return comparison, nil
}

func compareStableVersions(left, right string) (int, error) {
	leftParts, err := parseStableVersion(left)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q: %w", left, err)
	}
	rightParts, err := parseStableVersion(right)
	if err != nil {
		return 0, fmt.Errorf("invalid version %q: %w", right, err)
	}

	for index := range leftParts {
		if len(leftParts[index]) < len(rightParts[index]) {
			return -1, nil
		}
		if len(leftParts[index]) > len(rightParts[index]) {
			return 1, nil
		}
		if leftParts[index] < rightParts[index] {
			return -1, nil
		}
		if leftParts[index] > rightParts[index] {
			return 1, nil
		}
	}

	return 0, nil
}

func parseStableVersion(version string) ([3]string, error) {
	var parsed [3]string
	parts := strings.Split(version, ".")
	if len(parts) != len(parsed) {
		return parsed, errors.New("must use MAJOR.MINOR.PATCH")
	}

	for index, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return parsed, errors.New("components must be decimal integers without leading zeroes")
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return parsed, errors.New("components must be decimal integers without leading zeroes")
			}
		}
		parsed[index] = part
	}

	return parsed, nil
}

func parseReleasePublicKey(encoded string) (ed25519.PublicKey, error) {
	if encoded == "" {
		return nil, errors.New("release verification public key is not embedded")
	}

	pemBytes, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode release verification public key: %w", err)
	}

	block, rest := pem.Decode(pemBytes)
	if block == nil || block.Type != "PUBLIC KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("release verification public key must contain exactly one PUBLIC KEY PEM block")
	}

	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse release verification public key: %w", err)
	}
	publicKey, ok := parsed.(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return nil, errors.New("release verification public key must be Ed25519")
	}

	return append(ed25519.PublicKey(nil), publicKey...), nil
}

func (u *Updater) Apply(result *CheckResult) error {
	if err := validateUpdatePlatform(runtime.GOOS); err != nil {
		return err
	}
	if err := u.validateUpdateResult(result); err != nil {
		return err
	}

	publicKey, err := parseReleasePublicKey(u.publicKey)
	if err != nil {
		return err
	}

	execPath, err := u.executablePath()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	lock, err := acquireUpdateLock(execPath)
	if err != nil {
		return err
	}
	defer lock.Close()

	info, err := os.Stat(execPath)
	if err != nil {
		return fmt.Errorf("stat executable: %w", err)
	}
	installedVersion, err := readBinaryVersion(execPath)
	if err != nil {
		return fmt.Errorf("read installed binary version: %w", err)
	}
	if err := validateUpgrade(installedVersion, result.Latest); err != nil {
		return err
	}

	manifest, err := u.downloadBytes(result.ChecksumsURL, maxReleaseMetadata)
	if err != nil {
		return fmt.Errorf("download signed checksum manifest: %w", err)
	}
	signature, err := u.downloadBytes(result.SignatureURL, ed25519.SignatureSize)
	if err != nil {
		return fmt.Errorf("download checksum signature: %w", err)
	}

	expectedDigest, err := verifyReleaseManifest(publicKey, manifest, signature, result.AssetName)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(filepath.Dir(execPath), ".awg-server.update.*")
	if err != nil {
		return fmt.Errorf("create update file: %w", err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			os.Remove(tmpPath)
		}
	}()

	actualDigest, err := u.downloadBinary(result.DownloadURL, tmp)
	if err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync update file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close update file: %w", err)
	}
	if actualDigest != expectedDigest {
		return fmt.Errorf("downloaded binary checksum does not match signed manifest for %s", result.AssetName)
	}
	if err := os.Chmod(tmpPath, info.Mode().Perm()); err != nil {
		return fmt.Errorf("set update file mode: %w", err)
	}

	downloadedVersion, err := readBinaryVersion(tmpPath)
	if err != nil {
		return fmt.Errorf("read downloaded binary version: %w", err)
	}
	if downloadedVersion != result.Latest {
		return fmt.Errorf("downloaded binary reports %q, expected %q", downloadedVersion, result.Latest)
	}

	installedVersion, err = readBinaryVersion(execPath)
	if err != nil {
		return fmt.Errorf("recheck installed binary version: %w", err)
	}
	if err := validateUpgrade(installedVersion, result.Latest); err != nil {
		return err
	}

	if err := os.Rename(tmpPath, execPath); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}
	removeTemp = false

	return nil
}

func readBinaryVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return "", err
	}
	if len(output) == 0 || output[len(output)-1] != '\n' || strings.Count(string(output), "\n") != 1 {
		return "", fmt.Errorf("unsupported version output %q", strings.TrimSpace(string(output)))
	}

	version := strings.TrimSuffix(strings.TrimPrefix(string(output), "awg-server "), "\n")
	if string(output) != fmt.Sprintf("awg-server %s\n", version) {
		return "", fmt.Errorf("unsupported version output %q", strings.TrimSpace(string(output)))
	}
	if version == "dev" {
		return version, nil
	}
	if _, err := parseStableVersion(version); err != nil {
		return "", fmt.Errorf("unsupported version output %q: %w", strings.TrimSpace(string(output)), err)
	}

	return version, nil
}

func validateUpgrade(current, target string) error {
	comparison, err := compareCurrentVersion(current, target)
	if err != nil {
		return err
	}
	if comparison > 0 {
		return fmt.Errorf("release %s is older than installed version %s", target, current)
	}
	if comparison == 0 {
		return fmt.Errorf("release %s is not newer than installed version %s", target, current)
	}
	return nil
}

func (u *Updater) validateUpdateResult(result *CheckResult) error {
	if result == nil || !result.NeedsUpdate {
		return errors.New("update result does not request an update")
	}
	if _, err := parseStableVersion(result.Latest); err != nil {
		return fmt.Errorf("invalid update version %q: %w", result.Latest, err)
	}

	if err := validateUpgrade(u.current, result.Latest); err != nil {
		return err
	}

	expectedAsset := releaseAssetName(runtime.GOOS, runtime.GOARCH)
	if result.AssetName != expectedAsset {
		return fmt.Errorf("release asset %s does not match this host (%s)", result.AssetName, expectedAsset)
	}
	expectedURLs := map[string]string{
		result.AssetName:   result.DownloadURL,
		checksumAssetName:  result.ChecksumsURL,
		signatureAssetName: result.SignatureURL,
	}
	for name, actualURL := range expectedURLs {
		expectedURL := expectedAssetURL(result.Latest, name)
		if actualURL != expectedURL {
			return fmt.Errorf("release asset %s has unexpected download URL %q", name, actualURL)
		}
	}

	return nil
}

func (u *Updater) downloadBytes(url string, limit int64) ([]byte, error) {
	resp, err := u.client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %d", resp.StatusCode)
	}

	contents, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > limit {
		return nil, fmt.Errorf("download exceeds %d-byte limit", limit)
	}

	return contents, nil
}

func (u *Updater) downloadBinary(url string, destination *os.File) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	resp, err := u.client.Get(url)
	if err != nil {
		return digest, fmt.Errorf("download binary: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return digest, fmt.Errorf("download binary returned %d", resp.StatusCode)
	}

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(destination, hasher), io.LimitReader(resp.Body, maxReleaseBinary+1))
	if err != nil {
		return digest, fmt.Errorf("write update file: %w", err)
	}
	if written == 0 {
		return digest, errors.New("downloaded binary is empty")
	}
	if written > maxReleaseBinary {
		return digest, fmt.Errorf("downloaded binary exceeds %d-byte limit", maxReleaseBinary)
	}
	copy(digest[:], hasher.Sum(nil))

	return digest, nil
}

func verifyReleaseManifest(publicKey ed25519.PublicKey, manifest, signature []byte, selectedAsset string) ([sha256.Size]byte, error) {
	var selectedDigest [sha256.Size]byte
	if len(signature) != ed25519.SignatureSize || !ed25519.Verify(publicKey, manifest, signature) {
		return selectedDigest, errors.New("signed checksum manifest signature verification failed")
	}
	if !strings.HasSuffix(string(manifest), "\n") {
		return selectedDigest, errors.New("signed checksum manifest must end with a newline")
	}

	lines := strings.Split(strings.TrimSuffix(string(manifest), "\n"), "\n")
	if len(lines) != len(releaseAssetNames) {
		return selectedDigest, fmt.Errorf("signed checksum manifest must contain exactly %d assets", len(releaseAssetNames))
	}

	found := false
	for index, line := range lines {
		expectedName := releaseAssetNames[index]
		if len(line) != sha256.Size*2+2+len(expectedName) || line[sha256.Size*2:sha256.Size*2+2] != "  " || line[sha256.Size*2+2:] != expectedName {
			return selectedDigest, errors.New("signed checksum manifest has an unexpected asset set, order, or format")
		}
		digestText := line[:sha256.Size*2]
		for _, character := range digestText {
			if character < '0' || character > '9' && character < 'a' || character > 'f' {
				return selectedDigest, errors.New("signed checksum manifest digest must use lowercase hexadecimal")
			}
		}
		if expectedName == selectedAsset {
			decoded, err := hex.DecodeString(digestText)
			if err != nil {
				return selectedDigest, fmt.Errorf("decode signed checksum: %w", err)
			}
			copy(selectedDigest[:], decoded)
			found = true
		}
	}
	if !found {
		return selectedDigest, fmt.Errorf("signed checksum manifest is missing %s", selectedAsset)
	}

	return selectedDigest, nil
}
