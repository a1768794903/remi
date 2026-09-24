package main

import (
	"log"

	"remi/server/internal/api"
	"remi/server/internal/audio"
	"remi/server/internal/config"
	"remi/server/internal/storage"
	ws "remi/server/internal/websocket"
)

func main() {
	cfg := config.FromEnv()
	connections, err := storage.Open(cfg.MySQLDSN, cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	if err != nil {
		log.Fatalf("open storage: %v", err)
	}
	defer connections.Close()
	tracker := audio.NewTracker()
	audioHandler := ws.NewHandler(tracker)
	server := api.BuildServer(cfg, audioHandler)
	defer server.Stop()
	log.Printf("Remi API listening on %s", cfg.HTTPAddr)
	server.Start()
}
