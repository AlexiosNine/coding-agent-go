package main

import (
	"os"
	"strconv"
	"time"

	"github.com/alexioschen/cc-connect/goagent/channel"
	"github.com/alexioschen/cc-connect/goagent/channel/feishu"
)

// Config holds all runtime configuration for the agent.
type Config struct {
	Mode    string // "cli", "repl", or "bot"
	Addr    string // listen address for bot mode, e.g. ":8080"
	Feishu  feishu.Config
	Session channel.SessionConfig
}

// loadConfig reads configuration from environment variables.
// Priority: environment variables > defaults.
func loadConfig() Config {
	cfg := Config{
		Mode: getEnv("AGENT_MODE", "cli"),
		Addr: getEnv("AGENT_ADDR", ":8080"),
		Feishu: feishu.Config{
			AppID:       os.Getenv("FEISHU_APP_ID"),
			AppSecret:   os.Getenv("FEISHU_APP_SECRET"),
			VerifyToken: os.Getenv("FEISHU_VERIFY_TOKEN"),
			EncryptKey:  os.Getenv("FEISHU_ENCRYPT_KEY"),
		},
		Session: channel.SessionConfig{
			IdleTimeout: parseDuration(os.Getenv("SESSION_IDLE_TIMEOUT"), 30*time.Minute),
		},
	}
	return cfg
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseDuration(s string, fallback time.Duration) time.Duration {
	if s == "" {
		return fallback
	}
	// Try seconds first (plain integer), then Go duration string.
	if secs, err := strconv.Atoi(s); err == nil {
		return time.Duration(secs) * time.Second
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return fallback
}
