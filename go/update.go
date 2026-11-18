package main

// Updates as in the TypeScript app (src/youtube-music-updates.ts): the same latest.json format,
// written by `tarve release` and signed with the key in tarve-update.key, checked against the same
// Ed25519 public key. The Go host runs no app packs, so a release brings the whole executable:
// downloaded in the background beside this one, checked (size, SHA-256, executable header), and
// swapped in when the app quits (or right away from the notification), so playback never stops.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/omarcosr/tarve/go/tarve"
)

// version is set by the release build (-ldflags "-X main.version=1.2.3"); a dev build never updates.
var version = "0.0.0-dev"

const (
	appName         = "YouTube Music"
	updateURL       = "https://github.com/omarcosr/youtube-music/releases/latest/download/latest-go.json"
	updatePublicKey = "Ax9hM9qQ6h1Sm3QKHTbdWBCyadL4zLGH26UqO/6ig8Y="
	firstCheck      = 15 * time.Second
	checkEvery      = 6 * time.Hour
	maxManifest     = 1 << 20
)

type updateArtifact struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type updateManifest struct {
	Name      string                    `json:"name"`
	Version   string                    `json:"version"`
	Platforms map[string]updateArtifact `json:"platforms"`
}

// updates holds the downloaded executable until it is installed.
var updates struct {
	sync.Mutex
	file    string
	restart bool
}

// updatePlatform is `${process.platform}-${process.arch}`, as tarve release keys artifacts.
func updatePlatform() string {
	system := map[string]string{"windows": "win32"}[runtime.GOOS]
	if system == "" {
		system = runtime.GOOS
	}
	arch := map[string]string{"amd64": "x64", "386": "ia32"}[runtime.GOARCH]
	if arch == "" {
		arch = runtime.GOARCH
	}
	return system + "-" + arch
}

// selfUpdatable: a release build in a folder this user can write (not `go run`, not Program Files).
func selfUpdatable() (string, bool) {
	if strings.HasSuffix(version, "-dev") {
		return "", false
	}
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil || strings.Contains(exe, "go-build") {
		return "", false
	}
	probe, err := os.CreateTemp(filepath.Dir(exe), ".update-probe-*")
	if err != nil {
		return "", false
	}
	probe.Close()
	os.Remove(probe.Name())
	return exe, true
}

// watchUpdates checks soon after start and every few hours until the app closes.
func watchUpdates(app *tarve.App) {
	exe, ok := selfUpdatable()
	if !ok {
		return
	}
	os.Remove(exe + ".old")
	go func() {
		<-app.Ready()
		wait := firstCheck
		for {
			select {
			case <-app.Closed():
				return
			case <-time.After(wait):
			}
			wait = checkEvery
			next, err := downloadUpdate(exe)
			if err != nil {
				log.Printf("update check failed: %v", err)
				continue
			}
			if next != "" {
				app.Post(func() {
					app.Notify(fmt.Sprintf("Update %s ready", next), "It installs when you quit. Click to restart now.", func() {
						updates.Lock()
						updates.restart = true
						updates.Unlock()
						app.Close()
					})
				})
				return
			}
		}
	}()
}

// downloadUpdate fetches and verifies the manifest and, when it names a newer version for this
// platform, downloads it next to the executable. It returns that version, or "" when up to date.
func downloadUpdate(exe string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	body, final, err := fetch(ctx, updateURL, maxManifest)
	if err != nil {
		return "", err
	}
	var signed struct{ Payload, Signature string }
	if err := json.Unmarshal(body, &signed); err != nil {
		return "", errors.New("update manifest is not JSON")
	}
	key, err := base64.StdEncoding.DecodeString(updatePublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return "", errors.New("bad update public key")
	}
	signature, err := base64.StdEncoding.DecodeString(signed.Signature)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(key), []byte(signed.Payload), signature) {
		return "", errors.New("update manifest signature is invalid")
	}
	var manifest updateManifest
	if err := json.Unmarshal([]byte(signed.Payload), &manifest); err != nil {
		return "", errors.New("update manifest payload is not JSON")
	}
	if manifest.Name != "" && manifest.Name != appName {
		return "", fmt.Errorf("update manifest is for %s, not %s", manifest.Name, appName)
	}
	if compareVersions(manifest.Version, version) <= 0 {
		return "", nil
	}
	artifact, ok := manifest.Platforms[updatePlatform()]
	if !ok {
		return "", nil
	}
	link, err := final.Parse(artifact.URL)
	if err != nil || link.Scheme != "https" {
		return "", fmt.Errorf("update URL must be HTTPS: %s", artifact.URL)
	}
	data, _, err := fetch(ctx, link.String(), artifact.Size+1)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != artifact.Size || hex.EncodeToString(sum[:]) != strings.ToLower(artifact.SHA256) {
		return "", errors.New("downloaded update does not match the manifest")
	}
	if runtime.GOOS == "windows" && !bytes.HasPrefix(data, []byte("MZ")) {
		return "", errors.New("downloaded update is not a Windows executable")
	}
	file := exe + ".new"
	if err := os.WriteFile(file, data, 0o755); err != nil {
		return "", err
	}
	updates.Lock()
	updates.file = file
	updates.Unlock()
	return manifest.Version, nil
}

func fetch(ctx context.Context, link string, limit int64) ([]byte, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("update download failed: HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("update download is too large")
	}
	return data, res.Request.URL, err
}

// installUpdate swaps a downloaded executable in (Windows lets a running executable be renamed;
// the old one is removed on the next start) and, after "Restart now", starts it.
func installUpdate() {
	updates.Lock()
	file, restart := updates.file, updates.restart
	updates.Unlock()
	if file == "" {
		return
	}
	exe := strings.TrimSuffix(file, ".new")
	os.Remove(exe + ".old")
	if err := os.Rename(exe, exe+".old"); err != nil {
		log.Printf("update install failed: %v", err)
		return
	}
	if err := os.Rename(file, exe); err != nil {
		os.Rename(exe+".old", exe)
		log.Printf("update install failed: %v", err)
		return
	}
	if restart {
		if err := exec.Command(exe, os.Args[1:]...).Start(); err != nil {
			log.Printf("update restart failed: %v", err)
		}
	}
}

// compareVersions orders semantic versions (1.2.3, 1.2.3-beta.1) as @tarve/core does.
func compareVersions(a, b string) int {
	split := func(v string) ([]string, []string) {
		v = strings.SplitN(strings.TrimPrefix(v, "v"), "+", 2)[0]
		core, pre, _ := strings.Cut(v, "-")
		var tags []string
		if pre != "" {
			tags = strings.Split(pre, ".")
		}
		return strings.Split(core, "."), tags
	}
	part := func(x, y string) int {
		nx, ex := strconv.Atoi(x)
		ny, ey := strconv.Atoi(y)
		switch {
		case ex == nil && ey == nil:
			return nx - ny
		case ex == nil:
			return -1
		case ey == nil:
			return 1
		}
		return strings.Compare(x, y)
	}
	ca, pa := split(a)
	cb, pb := split(b)
	for i := 0; i < 3; i++ {
		x, y := "0", "0"
		if i < len(ca) {
			x = ca[i]
		}
		if i < len(cb) {
			y = cb[i]
		}
		if d := part(x, y); d != 0 {
			return d
		}
	}
	switch {
	case len(pa) == 0 && len(pb) == 0:
		return 0
	case len(pa) == 0:
		return 1
	case len(pb) == 0:
		return -1
	}
	for i := 0; i < len(pa) && i < len(pb); i++ {
		if d := part(pa[i], pb[i]); d != 0 {
			return d
		}
	}
	return len(pa) - len(pb)
}
