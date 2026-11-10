package common

import (
	"log"
	"os"
	"strings"
	"time"
)

// TokenTTL is how long a login token stays valid.
const TokenTTL = 24 * time.Hour

// Env returns the environment variable key, or fallback when it is unset or blank.
func Env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// DatabaseURL returns the PostgreSQL connection string.
func DatabaseURL() string {
	return Env("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/ecommerce?sslmode=disable")
}

// RedisAddr returns host:port of the Redis broker used for Pub/Sub.
func RedisAddr() string {
	return Env("REDIS_ADDR", "localhost:6379")
}

// JWTSecret returns the HMAC key shared by the gateway and every service.
func JWTSecret() []byte {
	secret := Env("JWT_SECRET", "")
	if secret == "" {
		log.Println("WARNING: JWT_SECRET is not set; using an insecure development default")
		secret = "dev-only-insecure-secret"
	}
	return []byte(secret)
}
