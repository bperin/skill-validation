package main

import (
	"fmt"
	"log"
	"net/http"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5e9}
	fmt.Println("project-validation listening on :8080")
	log.Fatal(server.ListenAndServe())
}
