package main

import (
	"os"

	"github.com/creditchain/creditchaind/api"
)

func main() {
	// Blockchain loading happens inside api.StartServer() —
	// calling api.LoadBlockchain() here would run before the
	// package-level `blockchain` var is initialized, causing a
	// nil-pointer panic on every startup.
	port := os.Getenv("PORT")
	if port == "" {
		port = ":8080"
	} else if port[0] != ':' {
		port = ":" + port
	}

	api.StartServer(port)
}
