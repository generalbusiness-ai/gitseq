// jsonata-inventory-ui serves the same-origin inventory spike on loopback.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/generalbusiness-ai/gitseq/host"
	"github.com/generalbusiness-ai/gitseq/spike/jsonataddl"
)

func main() {
	var repo, databaseDirectory, listen, credential string
	flag.StringVar(&repo, "repo", "", "Git repository bound to the inventory spike")
	flag.StringVar(&databaseDirectory, "database-dir", "", "directory for disposable projection databases")
	flag.StringVar(&listen, "listen", "127.0.0.1:7788", "loopback HTTP address")
	flag.StringVar(&credential, "session", "", "session credential; a random value is printed when omitted")
	flag.Parse()
	if repo == "" || databaseDirectory == "" {
		fmt.Fprintln(os.Stderr, "usage: jsonata-inventory-ui -repo PATH -database-dir PATH [-listen 127.0.0.1:7788] [-session SECRET]")
		os.Exit(2)
	}
	if err := requireLoopback(listen); err != nil {
		log.Fatal(err)
	}
	if credential == "" {
		data := make([]byte, 24)
		if _, err := rand.Read(data); err != nil {
			log.Fatal(err)
		}
		credential = hex.EncodeToString(data)
	}
	_, signer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	workspace, err := host.Open(context.Background(), repo, jsonataddl.InventoryApplication)
	if err != nil {
		log.Fatal(err)
	}
	handler, err := jsonataddl.NewInventoryUI(context.Background(), workspace, signer, databaseDirectory, credential)
	if err != nil {
		log.Fatal(err)
	}
	defer handler.Close()
	server := &http.Server{
		Addr: listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second,
	}
	log.Printf("inventory spike listening at http://%s/", listen)
	log.Printf("session credential: %s", credential)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

func requireLoopback(address string) error {
	hostName, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("listen address: %w", err)
	}
	if hostName == "localhost" {
		return nil
	}
	ip := net.ParseIP(hostName)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("inventory spike must listen on a loopback address")
	}
	return nil
}
