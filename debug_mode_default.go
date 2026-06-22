//go:build !js || !wasm

package main

import "os"

func initDebugMode() {
	v := os.Getenv("DEBUG")
	DebugMode = v == "true" || v == "1"
}
