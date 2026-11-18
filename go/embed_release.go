//go:build release

package main

import _ "embed"

// The release build (scripts/release.ts) copies the Tarve native library and the icon here first,
// so the downloaded .exe runs on its own.

//go:embed release/tarve_native
var embeddedNative []byte

//go:embed release/icon.png
var embeddedIcon []byte
