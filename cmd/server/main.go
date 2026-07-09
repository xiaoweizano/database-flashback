package main

import (
	"log"
	"net/http"
	"os"

	"github.com/a-shan/mysql-pitr/internal/server"
)

func main() {
	router := server.NewRouter()

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	log.Printf("mysql-pitr-server starting on %s", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("server: %v", err)
	}
}
