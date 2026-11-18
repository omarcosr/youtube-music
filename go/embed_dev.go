//go:build !release

package main

// Development builds find the native library through TARVE_NATIVE or native/bin/current.json.
var embeddedNative, embeddedIcon []byte
