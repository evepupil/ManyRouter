package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type Mode string

const (
	ModeServe   Mode = "serve"
	ModeWorker  Mode = "worker"
	ModeMigrate Mode = "migrate"
	ModeAll     Mode = "all"
)

type Config struct {
	Mode          Mode
	DatabaseURL   string
	HTTPAddress   string
	MasterKey     string
	OperatorToken string
	LogLevel      string
}

func Load(mode Mode) (Config, error) {
	if mode != ModeServe && mode != ModeWorker && mode != ModeMigrate && mode != ModeAll {
		return Config{}, fmt.Errorf("unsupported mode %q", mode)
	}
	config := Config{
		Mode:          mode,
		DatabaseURL:   strings.TrimSpace(os.Getenv("MANYROUTER_DATABASE_URL")),
		HTTPAddress:   strings.TrimSpace(os.Getenv("MANYROUTER_HTTP_ADDRESS")),
		MasterKey:     strings.TrimSpace(os.Getenv("MANYROUTER_MASTER_KEY")),
		OperatorToken: strings.TrimSpace(os.Getenv("MANYROUTER_OPERATOR_TOKEN")),
		LogLevel:      strings.ToLower(strings.TrimSpace(os.Getenv("MANYROUTER_LOG_LEVEL"))),
	}
	if config.DatabaseURL == "" {
		return Config{}, errors.New("MANYROUTER_DATABASE_URL is required")
	}
	if config.HTTPAddress == "" {
		config.HTTPAddress = "127.0.0.1:8080"
	}
	if config.LogLevel == "" {
		config.LogLevel = "info"
	}
	if config.LogLevel != "debug" && config.LogLevel != "info" && config.LogLevel != "warn" && config.LogLevel != "error" {
		return Config{}, errors.New("MANYROUTER_LOG_LEVEL must be debug, info, warn, or error")
	}
	if mode != ModeMigrate && config.MasterKey == "" {
		return Config{}, errors.New("MANYROUTER_MASTER_KEY is required")
	}
	if (mode == ModeServe || mode == ModeAll) && len(config.OperatorToken) < 32 {
		return Config{}, errors.New("MANYROUTER_OPERATOR_TOKEN must contain at least 32 characters")
	}
	return config, nil
}

func (c Config) ServesHTTP() bool {
	return c.Mode == ModeServe || c.Mode == ModeAll
}

func (c Config) RunsWorkers() bool {
	return c.Mode == ModeWorker || c.Mode == ModeAll
}
