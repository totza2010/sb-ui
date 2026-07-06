// Package selfupdate checks GitHub releases for a newer sb-ui binary and
// replaces the running binary in place (download -> atomic swap -> re-exec).
// Mirrors the way the Saltbox autoplow role pulls releases/latest.
package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"sb-ui/internal/buildinfo"
)

// DefaultRepo is the GitHub repo to pull releases from. Override with SB_UI_REPO.
const DefaultRepo = "totza2010/sb-ui"

func repo() string {
	if r := strings.TrimSpace(os.Getenv("SB_UI_REPO")); r != "" {
		return r
	}
	return DefaultRepo
}

// assetName is the release asset for the current platform, e.g. sb-ui-linux-amd64.
func assetName() string {
	return fmt.Sprintf("sb-ui-%s-%s", runtime.GOOS, runtime.GOARCH)
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"` // nightly builds put the git-describe version here
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Info is the result of a version check.
type Info struct {
	Current    string `json:"current"`
	Latest     string `json:"latest"`
	Channel    string `json:"channel"`
	Available  bool   `json:"update_available"`
	AssetURL   string `json:"asset_url,omitempty"`
	ReleaseURL string `json:"release_url,omitempty"`
	Asset      string `json:"asset"`
	Note       string `json:"note,omitempty"`
}

// getRelease fetches release metadata from GitHub (releases/latest, or a tag).
func getRelease(ctx context.Context, apiPath string) (*ghRelease, error) {
	url := "https://api.github.com/repos/" + repo() + apiPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github api: %s", resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// Check compares the running version against the newest release on the given channel
// ("stable" = releases/latest; "nightly" = the moving `nightly` pre-release built from
// master). Nightly versions are git-describe strings, so compared exactly.
func Check(ctx context.Context, channel string) Info {
	if channel != "nightly" {
		channel = "stable"
	}
	info := Info{Current: buildinfo.Version, Asset: assetName(), Channel: channel}

	apiPath := "/releases/latest"
	if channel == "nightly" {
		apiPath = "/releases/tags/nightly"
	}
	rel, err := getRelease(ctx, apiPath)
	if err != nil {
		info.Note = "could not reach GitHub (" + channel + "): " + err.Error()
		return info
	}
	info.Latest = rel.TagName
	if channel == "nightly" && strings.TrimSpace(rel.Name) != "" {
		info.Latest = rel.Name // the git-describe version baked into the nightly binary
	}
	info.ReleaseURL = rel.HTMLURL
	for _, a := range rel.Assets {
		if a.Name == info.Asset {
			info.AssetURL = a.URL
			break
		}
	}
	switch {
	case info.Current == "dev" || info.Current == "":
		info.Note = "development build — version comparison skipped"
	case info.AssetURL == "":
		info.Note = "no " + info.Asset + " asset in the " + channel + " release"
	case norm(info.Current) != norm(info.Latest):
		info.Available = true
	}
	return info
}

func norm(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }
