package main

import (
	"context"
	"log"
	"time"

	"github.com/Punith1117/ticket-forge/internal/database"
)

func main() {
	log.Println("TicketForge worker starting...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := database.ConfigFromEnv()
	pool, err := database.NewPool(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to initialize database connection pool: %v", err)
	}
	defer pool.Close()

	log.Println("TicketForge worker database connection pool established successfully.")
}
