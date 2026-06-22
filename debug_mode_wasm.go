//go:build js && wasm

package main

import "syscall/js"

func initDebugMode() {
	window := js.Global().Get("window")
	location := window.Get("location")
	search := location.Get("search").String()
	urlParams := js.Global().Get("URLSearchParams").New(search)
	debugParam := urlParams.Call("get", "debug").String()
	DebugMode = debugParam == "true" || debugParam == "1"
}
