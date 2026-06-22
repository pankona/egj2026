package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := ":18081"
	if v := os.Getenv("PORT"); v != "" {
		addr = ":" + v
	}
	fs := http.FileServer(http.Dir("dist"))
	http.Handle("/", fs)
	log.Printf("Listening on %s — http://localhost%s/", addr, addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
