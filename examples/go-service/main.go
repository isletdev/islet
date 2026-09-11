package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	started := time.Now()
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"greeting": os.Getenv("GREETING"), "uptimeSeconds": int(time.Since(started).Seconds())})
	})
	log.Println("listening on", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
