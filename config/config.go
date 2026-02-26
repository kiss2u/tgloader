package config

import (
	"fmt"
	"io/ioutil"
	"os"

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

// Load loads the configuration from config.yaml
func Load() (*Config, error) {
	// Check /app/config.yaml first (Docker), then local config.yaml
	configPath := "/app/config.yaml"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		configPath = "./config.yaml"
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			return nil, fmt.Errorf("config file not found: /app/config.yaml or ./config.yaml")
		}
	}

	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config

	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Check: either bot token OR (app_id + app_hash + phone/session_string)
	hasBotToken := config.Telegram.Token != ""
	hasMTProto := config.Telegram.AppID > 0 && config.Telegram.AppHash != "" &&
		(config.Telegram.Phone != "" || config.Telegram.SessionString != "")

	if !hasBotToken && !hasMTProto {
		return nil, fmt.Errorf("telegram config missing: either bot token or (app_id + app_hash + phone/session_string) required")
	}

	return &config, nil
}
