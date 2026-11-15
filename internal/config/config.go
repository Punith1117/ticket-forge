package config

import (
	"log"
	"os"

	"github.com/joho/godotenv"
)

// Load loads environment variables from the specified .env file.
//
// Existing environment variables are never overwritten.
// Missing .env files are allowed because production environments
// may provide configuration directly.
func Load(paths ...string) {
	if err := godotenv.Load(paths...); err != nil {
		if !os.IsNotExist(err) {
			log.Printf("Warning: could not load .env: %v", err)
		}
	}
}
