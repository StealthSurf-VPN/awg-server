package update

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

const expectedURLPrefix = "https://github.com/" + repo + "/"

const (
	repo       = "stealthsurf-vpn/awg-server"
	binaryName = "awg-server"
)

type Updater struct {
	current string
	client  *http.Client
}

type CheckResult struct {
	Latest      string
	DownloadURL string
	NeedsUpdate bool
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
		current: currentVersion,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (u *Updater) Check() (*CheckResult, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)

	resp, err := u.client.Get(url)
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

	latest := strings.TrimPrefix(rel.TagName, "v")
	current := strings.TrimPrefix(u.current, "v")

	if current != "dev" && current == latest {
		return &CheckResult{Latest: latest}, nil
	}

	assetName := fmt.Sprintf("%s-%s-%s", binaryName, runtime.GOOS, runtime.GOARCH)

	for _, a := range rel.Assets {
		if a.Name == assetName {
			return &CheckResult{
				Latest:      latest,
				DownloadURL: a.BrowserDownloadURL,
				NeedsUpdate: true,
			}, nil
		}
	}

	return nil, fmt.Errorf("no asset found for %s", assetName)
}

func (u *Updater) Apply(downloadURL string) error {
	if !strings.HasPrefix(strings.ToLower(downloadURL), strings.ToLower(expectedURLPrefix)) {
		return fmt.Errorf("untrusted download URL: %s", downloadURL)
	}

	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}

	info, err := os.Stat(execPath)
	if err != nil {
		return fmt.Errorf("stat executable: %w", err)
	}

	tmpPath := execPath + ".update"

	resp, err := u.client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("download binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write binary: %w", err)
	}

	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmpPath, execPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("replace binary: %w", err)
	}

	return nil
}
