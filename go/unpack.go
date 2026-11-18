package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
)

// unpackRelease writes the native library and icon a release build embeds into the user's cache
// (once per content) and points Tarve at that library. It returns the icon path, or "".
func unpackRelease() string {
	if len(embeddedNative) == 0 {
		return ""
	}
	sum := sha256.Sum256(embeddedNative)
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	dir := filepath.Join(cache, "YouTubeMusic", "native", hex.EncodeToString(sum[:6]))
	name := map[string]string{"windows": "tarve_native.dll", "darwin": "libtarve_native.dylib"}[runtime.GOOS]
	if name == "" {
		name = "libtarve_native.so"
	}
	library, icon := filepath.Join(dir, name), filepath.Join(dir, "icon.png")
	if os.MkdirAll(dir, 0o755) != nil || !written(library, embeddedNative) || !written(icon, embeddedIcon) {
		return ""
	}
	if os.Getenv("TARVE_NATIVE") == "" {
		os.Setenv("TARVE_NATIVE", library)
	}
	return icon
}

// written makes sure path holds data, writing it through a temporary file when it does not.
func written(path string, data []byte) bool {
	if info, err := os.Stat(path); err == nil && info.Size() == int64(len(data)) {
		return true
	}
	temporary := path + ".tmp"
	if os.WriteFile(temporary, data, 0o644) != nil {
		return false
	}
	return os.Rename(temporary, path) == nil
}
