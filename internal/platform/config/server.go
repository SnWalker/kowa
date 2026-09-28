package config

import "os"

const defaultServerAddress = "127.0.0.1:8080"

// Server contains process-level HTTP server configuration.
type Server struct {
	Address string
}

// NewServer loads server configuration from the process environment.
func NewServer() Server {
	address := os.Getenv("KOWA_SERVER_ADDR")
	if address == "" {
		address = defaultServerAddress
	}

	return Server{Address: address}
}
