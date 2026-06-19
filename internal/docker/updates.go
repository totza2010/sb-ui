package docker

import (
	"encoding/json"
	"strings"
	"sync"
	"time"

	"sb-ui/internal/store"
)

// Update cache: image → outdated (true/false) | unknown (absent). Persisted.
var (
	updMu    sync.Mutex
	updCache = map[string]*bool{}
	updTS    = map[string]float64{}
	lastChk  *float64
)

const cacheRel = "cache/image_updates.json"

type cacheFile struct {
	Results     map[string]*bool   `json:"results"`
	TS          map[string]float64 `json:"ts"`
	LastChecked *float64           `json:"last_checked"`
}

func LoadCache() {
	var cf cacheFile
	store.ReadJSON(cacheRel, &cf)
	updMu.Lock()
	defer updMu.Unlock()
	if cf.Results != nil {
		updCache = cf.Results
	}
	if cf.TS != nil {
		updTS = cf.TS
	}
	lastChk = cf.LastChecked
}

func saveCache() {
	updMu.Lock()
	cf := cacheFile{Results: updCache, TS: updTS, LastChecked: lastChk}
	updMu.Unlock()
	store.WriteJSON(cacheRel, cf)
}

// CachedUpdates returns image → outdated (nil = unknown), for the frontend.
func CachedUpdates() map[string]*bool {
	updMu.Lock()
	defer updMu.Unlock()
	out := make(map[string]*bool, len(updCache))
	for k, v := range updCache {
		out[k] = v
	}
	return out
}

func UpdatesMeta() map[string]any {
	updMu.Lock()
	defer updMu.Unlock()
	ts := make(map[string]float64, len(updTS))
	for k, v := range updTS {
		ts[k] = v
	}
	return map[string]any{"last_checked": lastChk, "ts": ts}
}

func GetImageCreated(image string) string {
	rc, out := run("docker", "image", "inspect", image, "--format", "{{.Created}}")
	if rc != 0 {
		return ""
	}
	return strings.TrimSpace(out)
}

// CheckImageUpdate: true=update available, false=current, nil=unknown.
// Compares the local image config digest (.Id) against the registry manifest.
func CheckImageUpdate(image string) *bool {
	rc, localID := run("docker", "image", "inspect", image, "--format", "{{.Id}}")
	if rc != 0 || strings.TrimSpace(localID) == "" {
		return nil
	}
	local := strings.TrimSpace(localID)

	rc, manRaw := run("docker", "manifest", "inspect", image)
	if rc != 0 || strings.TrimSpace(manRaw) == "" {
		return nil
	}
	var man struct {
		MediaType string `json:"mediaType"`
		Config    struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				OS   string `json:"os"`
				Arch string `json:"architecture"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if json.Unmarshal([]byte(manRaw), &man) != nil {
		return nil
	}

	remoteConfig := man.Config.Digest
	if strings.Contains(man.MediaType, "list") || strings.Contains(man.MediaType, "index") {
		_, platRaw := run("docker", "info", "--format", "{{.OSType}}/{{.Architecture}}")
		platform := strings.TrimSpace(platRaw)
		if platform == "" {
			platform = "linux/amd64"
		}
		var sub string
		for _, m := range man.Manifests {
			if m.Platform.OS+"/"+m.Platform.Arch == platform {
				sub = m.Digest
				break
			}
		}
		if sub == "" && len(man.Manifests) > 0 {
			sub = man.Manifests[0].Digest
		}
		if sub == "" {
			return nil
		}
		base := strings.SplitN(strings.SplitN(image, "@", 2)[0], ":", 2)[0]
		rc, subRaw := run("docker", "manifest", "inspect", base+"@"+sub)
		if rc != 0 {
			return nil
		}
		var subMan struct {
			Config struct {
				Digest string `json:"digest"`
			} `json:"config"`
		}
		if json.Unmarshal([]byte(subRaw), &subMan) != nil {
			return nil
		}
		remoteConfig = subMan.Config.Digest
	}
	if remoteConfig == "" {
		return nil
	}
	res := remoteConfig != local
	updMu.Lock()
	updCache[image] = &res
	updTS[image] = float64(time.Now().Unix())
	updMu.Unlock()
	return &res
}

// CheckAllUpdates checks images with bounded concurrency, updates + saves cache.
// SaveCache persists the update cache (e.g. after a single-image recheck).
func SaveCache() { saveCache() }

func CheckAllUpdates(images []string) map[string]*bool {
	sem := make(chan struct{}, 3)
	var wg sync.WaitGroup
	out := map[string]*bool{}
	var outMu sync.Mutex
	now := float64(time.Now().Unix())
	for _, img := range images {
		wg.Add(1)
		go func(img string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := CheckImageUpdate(img)
			outMu.Lock()
			out[img] = res
			outMu.Unlock()
			updMu.Lock()
			updCache[img] = res
			updTS[img] = now
			updMu.Unlock()
		}(img)
	}
	wg.Wait()
	updMu.Lock()
	lastChk = &now
	updMu.Unlock()
	saveCache()
	return out
}
