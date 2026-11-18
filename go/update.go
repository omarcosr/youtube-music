package main

// Updates as in the TypeScript app (src/youtube-music-updates.ts): the same latest.json format,
// written by `tarve release` (scripts/release.ts) and signed with youtube-music-update.key, checked
// against the same Ed25519 public key. The Go host runs no app packs, so a release brings the whole
// executable: downloaded in the background beside this one, checked (size, SHA-256, executable
// header), and swapped in when the app quits (or right away from About or the notification), so
// playback never stops. About (account menu) shows where it stands.

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
	"github.com/omarcosr/youtube-music/go/ytm"
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

// httpClient fetches the manifest and the executable; tests point it at a local TLS server.
var httpClient = http.DefaultClient

// updater is the update cycle: a check, a download with progress, then the executable ready
// beside this one until the app quits (or restarts into it).
type updater struct {
	exe       string
	url       string
	publicKey string
	set       func(ytm.UpdateStatus)
	announce  func(version string)

	mu      sync.Mutex
	busy    bool
	file    string
	ready   string
	restart bool
}

// updates is the running app's updater; nil when this copy does not update itself.
var updates *updater

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

// watchUpdates shows this build in About and checks soon after start and every few hours.
func watchUpdates(app *tarve.App) {
	st := ytm.St()
	set := func(s ytm.UpdateStatus) {
		app.Post(func() {
			st.Update = &s
			st.Refresh()
		})
	}
	app.Post(func() { st.AppVersion, st.Flavor = version, "Go" })
	exe, ok := selfUpdatable()
	if !ok {
		set(ytm.UpdateStatus{Status: "off"})
		return
	}
	os.Remove(exe + ".old")
	u := &updater{exe: exe, url: updateURL, publicKey: updatePublicKey, set: set, announce: func(next string) {
		app.Post(func() {
			// About shows it when open; otherwise a notification says so, once.
			if st.About {
				return
			}
			app.Notify(fmt.Sprintf("Update %s ready", next), "It installs when you quit. Click to restart now.", func() {
				st.RestartToUpdate()
			})
		})
	}}
	updates = u
	app.Post(func() {
		st.CheckUpdates = func() { go u.check(true) }
		st.RestartToUpdate = func() {
			u.mu.Lock()
			u.restart = true
			u.mu.Unlock()
			app.Close()
		}
	})
	set(ytm.UpdateStatus{Status: "idle"})
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
			u.check(false)
		}
	}()
}

// check fetches and verifies the manifest and, when it names a newer version for this
// platform, downloads it next to the executable. manual: About's button (no notification).
func (u *updater) check(manual bool) {
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return
	}
	if u.ready != "" {
		u.mu.Unlock()
		u.set(ytm.UpdateStatus{Status: "ready", Version: u.ready})
		return
	}
	u.busy = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.busy = false
		u.mu.Unlock()
	}()
	u.set(ytm.UpdateStatus{Status: "checking"})
	next, err := u.download()
	switch {
	case err != nil:
		// Offline, a release still uploading or a rejected signature: About says so, and the next round tries again.
		log.Printf("update failed: %v", err)
		u.set(ytm.UpdateStatus{Status: "error", Error: err.Error()})
	case next == "":
		u.set(ytm.UpdateStatus{Status: "current"})
	default:
		u.set(ytm.UpdateStatus{Status: "ready", Version: next})
		if !manual {
			u.announce(next)
		}
	}
}

// download returns the version it downloaded, or "" when this one is the newest.
func (u *updater) download() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	body, final, err := fetch(ctx, u.url, maxManifest, nil)
	if err != nil {
		return "", err
	}
	var signed struct{ Payload, Signature string }
	if err := json.Unmarshal(body, &signed); err != nil {
		return "", errors.New("update manifest is not JSON")
	}
	key, err := base64.StdEncoding.DecodeString(u.publicKey)
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
	next := manifest.Version
	u.set(ytm.UpdateStatus{Status: "downloading", Version: next, Progress: ptr(0)})
	shown := 0
	data, _, err := fetch(ctx, link.String(), artifact.Size, func(received int64) {
		if artifact.Size <= 0 {
			return
		}
		if percent := int(received * 100 / artifact.Size); percent != shown && percent <= 100 {
			shown = percent
			u.set(ytm.UpdateStatus{Status: "downloading", Version: next, Progress: ptr(float64(percent) / 100)})
		}
	})
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
	file := u.exe + ".new"
	if err := os.WriteFile(file, data, 0o755); err != nil {
		return "", err
	}
	u.mu.Lock()
	u.file, u.ready = file, next
	u.mu.Unlock()
	return next, nil
}

func ptr(f float64) *float64 { return &f }

// fetch reads at most limit bytes of link, reporting progress as it goes.
func fetch(ctx context.Context, link string, limit int64, progress func(received int64)) ([]byte, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, nil, err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("HTTP %d from %s", res.StatusCode, res.Request.URL.Host)
	}
	var data []byte
	buf := make([]byte, 64<<10)
	for {
		n, err := res.Body.Read(buf)
		data = append(data, buf[:n]...)
		if int64(len(data)) > limit {
			return nil, nil, errors.New("update download is too large")
		}
		if progress != nil && n > 0 {
			progress(int64(len(data)))
		}
		if err == io.EOF {
			return data, res.Request.URL, nil
		}
		if err != nil {
			return nil, nil, err
		}
	}
}

// installUpdate swaps a downloaded executable in (Windows lets a running executable be renamed;
// the old one is removed on the next start) and, after "Restart now", starts it.
func installUpdate() {
	if updates != nil {
		updates.install()
	}
}

func (u *updater) install() {
	u.mu.Lock()
	file, restart := u.file, u.restart
	u.file, u.ready = "", ""
	u.mu.Unlock()
	if file == "" {
		return
	}
	exe := u.exe
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
