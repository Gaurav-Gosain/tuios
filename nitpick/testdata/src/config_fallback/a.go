package config_fallback

import (
	"cmp"
	"os"
)

func host() string {
	h := os.Getenv("DB_HOST")
	if h == "" {
		h = "localhost" // want "DB_HOST falls back to the development value \"localhost\""
	}
	return h
}

func url() string {
	if v := os.Getenv("API_URL"); v == "" {
		return "http://127.0.0.1:8080" // want "API_URL falls back"
	}
	return os.Getenv("API_URL")
}

func dsn() string {
	return cmp.Or(os.Getenv("DSN"), "postgres://localhost/app?sslmode=disable") // want "DSN falls back"
}

func port() string {
	addr := "localhost:9000" // want "PORT falls back"
	if v := os.Getenv("PORT"); v != "" {
		addr = v
	}
	return addr
}

func password() string {
	p, ok := os.LookupEnv("DB_PASSWORD")
	if !ok {
		p = "changeme" // want "DB_PASSWORD falls back"
	}
	return p
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func redis() string { return getenv("REDIS_ADDR", "127.0.0.1:6379") } // want "REDIS_ADDR falls back"

// Not reported: a default that suits production, and a read that fails
// when the variable is not set.

func region() string { return getenv("REGION", "eu-west-1") }

func home() string {
	h := os.Getenv("HOME")
	if h == "" {
		h = "/"
	}
	return h
}

func must() string {
	v := os.Getenv("SECRET_KEY")
	if v == "" {
		panic("SECRET_KEY is not set")
	}
	return v
}

func addr() string {
	if v, ok := os.LookupEnv("ADDR"); ok {
		return v
	}
	return ":8080"
}
