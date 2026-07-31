package main

import (
	"log"
	"net/http"
	"os"

	"github.com/a-shan/mysql-pitr/internal/server"
)

func main() {
	srv, err := server.New()
	if err != nil {
		log.Fatalf("server: init: %v", err)
	}
	defer srv.Close()

	webAddr := os.Getenv("LISTEN_ADDR")
	if webAddr == "" {
		webAddr = ":8080"
	}

	agentAddr := os.Getenv("AGENT_LISTEN_ADDR")
	if agentAddr == "" {
		agentAddr = ":9443"
	}

	go func() {
		log.Printf("mysql-pitr-server agent endpoint listening on %s (mTLS)", agentAddr)
		// The server certificate is provided via TLSConfig; certFile/keyFile
		// are intentionally empty.
		tlsServer := &http.Server{
			Addr:      agentAddr,
			Handler:   srv.Agent,
			TLSConfig: srv.TLSConfig,
		}
		if err := tlsServer.ListenAndServeTLS("", ""); err != nil {
			log.Fatalf("server: agent endpoint: %v", err)
		}
	}()

	log.Printf("mysql-pitr-server starting on %s", webAddr)
	if err := http.ListenAndServe(webAddr, srv.Web); err != nil {
		log.Fatalf("server: %v", err)
	}
}
