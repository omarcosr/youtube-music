// A YouTube Music client on the Go host: home feed, search, albums, playlists and artists, a play
// queue, your library after signing in with Google (a browser window, or pasted cookies), and
// synced lyrics on the now-playing page. The same app as the TypeScript one in src/, node for node.
// Personal-use demo: YouTube's Terms of Service forbid third-party clients.
package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/omarcosr/tarve/go/tarve"
	"github.com/omarcosr/youtube-music/go/ytm"
)

// iconPath finds assets/icon.png from the repository or icon.png next to the executable.
func iconPath() string {
	for _, dir := range []string{".", ".."} {
		p := filepath.Join(dir, "assets", "icon.png")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if exe, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exe), "icon.png")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func main() {
	// One app per user: closed to the tray, a later launch brings this one back instead of starting
	// another player with a second media session.
	second := make(chan struct{}, 1)
	instance := requestSingleInstance("YouTubeMusic.Desktop", func() {
		select {
		case second <- struct{}{}:
		default:
		}
	})
	if instance == nil {
		return
	}
	// A release build carries its native library and icon; a dev build finds them in the repository.
	icon := unpackRelease()
	if icon == "" {
		icon = iconPath()
	}
	// Name, icon and app id match package.json "tarve".
	app, err := tarve.CreateApp(func() tarve.Child { return ytm.YouTubeMusicView() }, tarve.Options{
		CloseBehavior: "hide",
		AppIdentity:   &tarve.AppIdentity{ID: "YouTubeMusic.Desktop", Name: "YouTube Music", Icon: icon},
	})
	if err != nil {
		log.Fatal(err)
	}
	// The queue, song, position, volume and modes of the last run come back, paused.
	ytm.RestoreSession()
	app.Post(func() {
		ytm.Connect(app)
	})
	go func() {
		saving := time.NewTicker(5 * time.Second)
		defer saving.Stop()
		for {
			select {
			case <-saving.C:
				app.Post(ytm.SaveSession)
			case <-app.Closed():
				return
			}
		}
	}()
	go func() {
		<-app.Ready()
		app.Post(func() {
			app.Tray(tarve.TrayOptions{
				Tooltip:       "YouTube Music",
				OnDoubleClick: app.Show,
				Menu: []tarve.TrayMenuItem{
					{ID: "show", Label: "Open", OnSelect: app.Show},
					{Separator: true},
					{ID: "quit", Label: "Quit", OnSelect: app.Close},
				},
			})
		})
	}()
	go func() {
		for {
			select {
			case <-second:
				app.Post(func() { app.Show(); app.Activate() })
			case <-app.Closed():
				return
			}
		}
	}()
	watchUpdates(app)
	err = app.Run()
	ytm.SaveSession()
	// A restarted version (an update) must become the first instance, not hand itself over to this one.
	instance.release()
	// A downloaded update replaces this executable now and runs on the next launch.
	installUpdate()
	if err != nil {
		log.Fatal(err)
	}
}
