package main

import (
	"log"

	yapyap "yapyap/server"
)

func main() {
	cfg, err := yapyap.LoadConfig("config.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	server := yapyap.NewYapYap(cfg)
	server.Start()
}
