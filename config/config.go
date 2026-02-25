package config

import (
	"fmt"
	"io/ioutil"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config holds all application configuration
type Config struct {
	Telegram  TelegramConfig `yaml:"telegram" json:"telegram"`
	Storage   StorageConfig  `yaml:"storage" json:"storage"`
	Downloads DownloadConfig `yaml:"downloads" json:"downloads"`
}

// TelegramConfig holds Telegram bot specific settings
type TelegramConfig struct {
	Token          string  `yaml:"token" json:"token"`
	AllowedChatIDs []int64 `yaml:"allowed_chat_ids" json:"allowed_chat_ids"`
	AppID          int64   `yaml:"app_id" json:"app_id"`
	AppHash        string  `yaml:"app_hash" json:"app_hash"`
	Phone          string  `yaml:"phone" json:"phone"`
	SessionString  string  `yaml:"session_string" json:"session_string"`
}

// GetToken returns the bot token
func (t *TelegramConfig) GetToken() string {
	return t.Token
}

// StorageConfig manages file storage settings
type StorageConfig struct {
	BasePath   string            `yaml:"base_path" json:"base_path"`
	TempPath   string            `yaml:"temp_path" json:"temp_path"`
	Categories map[string]string `yaml:"categories" json:"categories"`
}

// DownloadConfig manages download behavior settings
type DownloadConfig struct {
	ConcurrentLimit int `yaml:"concurrent_limit" json:"concurrent_limit"`
	Timeout         int `yaml:"timeout" json:"timeout"`
}

// Load loads the configuration from a YAML file
func Load() (*Config, error) {
	configPath := getEnv("CONFIG_PATH", "./config.yaml")

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return loadDefaults(), nil
	}

	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config

	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	applyEnvironmentOverrides(&config)

	// Check: either bot token OR (app_id + app_hash + phone/session_string)
	hasBotToken := config.Telegram.Token != "" || getEnv("TELEGRAM_TOKEN", "") != ""
	hasMTProto := config.Telegram.AppID > 0 && config.Telegram.AppHash != "" &&
		(config.Telegram.Phone != "" || config.Telegram.SessionString != "")

	if !hasBotToken && !hasMTProto {
		return nil, fmt.Errorf("telegram config missing: either bot token or (app_id + app_hash + phone/session_string) required")
	}

	return &config, nil
}

// loadDefaults creates a configuration with default values
func loadDefaults() *Config {
	return &Config{
		Telegram: TelegramConfig{
			Token:          "",
			AllowedChatIDs: []int64{},
			AppID:          0,
			AppHash:        "",
			Phone:          "",
			SessionString:  "",
		},
		Storage: StorageConfig{
			BasePath: "./downloads",
			TempPath: "./tmp",
			Categories: map[string]string{
				"documents": "Documents",
				"archives":  "Archives",
				"videos":    "Videos",
				"audio":     "Audio",
				"images":    "Images",
				"ebooks":    "Ebooks",
				"software":  "Software",
			},
		},
		Downloads: DownloadConfig{
			ConcurrentLimit: 5,
			Timeout:         30,
		},
	}
}

// applyEnvironmentOverrides applies environment variable settings
func applyEnvironmentOverrides(config *Config) {
	if token := getEnv("TELEGRAM_TOKEN", ""); token != "" {
		config.Telegram.Token = token
	}

	if storagePath := getEnv("STORAGE_PATH", ""); storagePath != "" {
		config.Storage.BasePath = storagePath
	}

	if maxConcurrentStr := getEnv("MAX_CONCURRENT_DOWNLOADS", ""); maxConcurrentStr != "" {
		if max, err := strconv.Atoi(maxConcurrentStr); err == nil {
			config.Downloads.ConcurrentLimit = max
		}
	}

	if appID := getEnv("TELEGRAM_APP_ID", ""); appID != "" {
		if id, err := strconv.ParseInt(appID, 10, 64); err == nil {
			config.Telegram.AppID = id
		}
	}

	if appHash := getEnv("TELEGRAM_APP_HASH", ""); appHash != "" {
		config.Telegram.AppHash = appHash
	}

	if phone := getEnv("TELEGRAM_PHONE", ""); phone != "" {
		config.Telegram.Phone = phone
	}

	if sessionString := getEnv("TELEGRAM_SESSION_STRING", ""); sessionString != "" {
		config.Telegram.SessionString = sessionString
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
