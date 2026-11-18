package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/omarcosr/youtube-music/go/ytm"
)

// release serves a signed manifest laid out as scripts/release.ts publishes latest-go.json.
type release struct {
	server   *httptest.Server
	key      ed25519.PrivateKey
	public   string
	manifest []byte
	exe      []byte
}

func newRelease(t *testing.T) *release {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	r := &release{key: private, public: base64.StdEncoding.EncodeToString(public), exe: append([]byte("MZ"), make([]byte, 300<<10)...)}
	r.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/latest-go.json" {
			w.Write(r.manifest)
			return
		}
		w.Write(r.exe)
	}))
	t.Cleanup(r.server.Close)
	previous := httpClient
	httpClient = r.server.Client()
	t.Cleanup(func() { httpClient = previous })
	return r
}

func (r *release) publish(t *testing.T, v, name string, exe []byte) {
	sum := sha256.Sum256(exe)
	payload, _ := json.Marshal(map[string]any{"name": name, "version": v, "pubDate": "2026-11-19T00:00:00Z",
		"platforms": map[string]any{updatePlatform(): map[string]any{"url": r.server.URL + "/YouTubeMusic-go-win32-x64.exe", "sha256": hex.EncodeToString(sum[:]), "size": len(exe)}}})
	r.manifest, _ = json.Marshal(map[string]string{"payload": string(payload), "signature": base64.StdEncoding.EncodeToString(ed25519.Sign(r.key, payload))})
}

type recorder struct {
	sync.Mutex
	statuses  []ytm.UpdateStatus
	announced []string
}

func (r *recorder) last() ytm.UpdateStatus {
	r.Lock()
	defer r.Unlock()
	return r.statuses[len(r.statuses)-1]
}

func newUpdater(t *testing.T, rel *release, current string) (*updater, *recorder) {
	previous := version
	version = current
	t.Cleanup(func() { version = previous })
	exe := filepath.Join(t.TempDir(), "YouTubeMusic.exe")
	os.WriteFile(exe, []byte("MZ old"), 0o755)
	rec := &recorder{}
	u := &updater{exe: exe, url: rel.server.URL + "/latest-go.json", publicKey: rel.public,
		set:      func(s ytm.UpdateStatus) { rec.Lock(); rec.statuses = append(rec.statuses, s); rec.Unlock() },
		announce: func(v string) { rec.Lock(); rec.announced = append(rec.announced, v); rec.Unlock() }}
	return u, rec
}

func TestUpdateDownloadsAndInstallsOnQuit(t *testing.T) {
	rel := newRelease(t)
	rel.publish(t, "0.1.5", appName, rel.exe)
	u, rec := newUpdater(t, rel, "0.1.4")
	u.check(false)
	if got := rec.last(); got.Status != "ready" || got.Version != "0.1.5" {
		t.Fatalf("status %+v", got)
	}
	if len(rec.announced) != 1 {
		t.Fatalf("announced %v", rec.announced)
	}
	progress := []float64{}
	for _, s := range rec.statuses {
		if s.Status == "downloading" && s.Progress != nil {
			progress = append(progress, *s.Progress)
		}
	}
	if len(progress) < 3 || progress[0] != 0 || progress[len(progress)-1] != 1 {
		t.Fatalf("progress %v", progress)
	}
	// A second check keeps the download instead of fetching it again.
	u.check(true)
	if got := rec.last(); got.Status != "ready" {
		t.Fatalf("status after ready %+v", got)
	}
	u.install()
	data, _ := os.ReadFile(u.exe)
	if string(data) != string(rel.exe) {
		t.Fatal("the executable was not replaced")
	}
	if old, _ := os.ReadFile(u.exe + ".old"); string(old) != "MZ old" {
		t.Fatal("the old executable was not kept as .old")
	}
	u.install()
}

func TestUpdateUpToDate(t *testing.T) {
	rel := newRelease(t)
	rel.publish(t, "0.1.4", appName, rel.exe)
	u, rec := newUpdater(t, rel, "0.1.4")
	u.check(true)
	if got := rec.last(); got.Status != "current" {
		t.Fatalf("status %+v", got)
	}
}

func TestUpdateRefusals(t *testing.T) {
	rel := newRelease(t)
	cases := map[string]func(){
		"another key":       func() { rel.public = base64.StdEncoding.EncodeToString(make([]byte, 32)) },
		"another app":       func() { rel.publish(t, "0.1.5", "Another App", rel.exe) },
		"wrong size/hash":   func() { rel.publish(t, "0.1.5", appName, append([]byte("MZ"), 1, 2, 3)) },
		"not a Windows exe": func() { rel.exe = []byte("not an exe"); rel.publish(t, "0.1.5", appName, rel.exe) },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			saved, savedExe := rel.public, rel.exe
			defer func() { rel.public, rel.exe = saved, savedExe }()
			rel.publish(t, "0.1.5", appName, rel.exe)
			spoil()
			u, rec := newUpdater(t, rel, "0.1.4")
			u.check(true)
			if got := rec.last(); got.Status != "error" || got.Error == "" {
				t.Fatalf("status %+v", got)
			}
			if _, err := os.Stat(u.exe + ".new"); err == nil {
				t.Fatal("a refused update was written")
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		sign int
	}{{"0.1.5", "0.1.4", 1}, {"0.1.4", "0.1.4", 0}, {"0.1.10", "0.1.9", 1}, {"1.0.0", "1.0.0-beta.1", 1}, {"1.0.0-beta.2", "1.0.0-beta.10", -1}, {"0.2.0", "0.10.0", -1}} {
		got := compareVersions(c.a, c.b)
		if (got > 0) != (c.sign > 0) || (got < 0) != (c.sign < 0) {
			t.Errorf("compareVersions(%s, %s) = %d", c.a, c.b, got)
		}
	}
}
