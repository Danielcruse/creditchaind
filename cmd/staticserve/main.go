// staticserve is the plain static-file server for website/ - separate
// from the creditchaind blockchain node (api.StartServer), matching
// the two-systemd-service split already running in production
// (creditchain.service = the node, creditchain-web.service = this).
//
// Recreated from scratch: the binary already running in production
// had no matching source anywhere in this repo, the project's backup
// zips, or its sibling working copies - if that binary were ever lost,
// there was no way to rebuild it. This restores that.
package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	dir := os.Getenv("WEBSITE_DIR")
	if dir == "" {
		dir = "website"
	}

	log.Printf("staticserve: serving %s on :%s\n", dir, port)
	log.Fatal(http.ListenAndServe(":"+port, http.FileServer(http.Dir(dir))))
}
