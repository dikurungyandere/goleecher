package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	APIID      int
	APIHash    string
	BotToken   string
	AdminID    int64
	AllowedIDs []int64
	WebEnabled bool
	Port       string
	TempDir    string
}

func Load() *Config {
	apiID, _ := strconv.Atoi(os.Getenv("API_ID"))
	adminID, _ := strconv.ParseInt(os.Getenv("ADMIN_ID"), 10, 64)

	var allowedIDs []int64
	if ids := os.Getenv("ALLOWED_IDS"); ids != "" {
		for _, s := range strings.Split(ids, ",") {
			s = strings.TrimSpace(s)
			if id, err := strconv.ParseInt(s, 10, 64); err == nil {
				allowedIDs = append(allowedIDs, id)
			}
		}
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	tempDir := os.Getenv("TEMP_DIR")
	if tempDir == "" {
		tempDir = "/tmp/goleecher"
	}

	return &Config{
		APIID:      apiID,
		APIHash:    os.Getenv("API_HASH"),
		BotToken:   os.Getenv("BOT_TOKEN"),
		AdminID:    adminID,
		AllowedIDs: allowedIDs,
		WebEnabled: strings.ToLower(strings.TrimSpace(os.Getenv("WEB_ENABLED"))) == "true",
		Port:       port,
		TempDir:    tempDir,
	}
}
