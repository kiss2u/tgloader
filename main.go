package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"tgloader/bot"
	"tgloader/config"
	"tgloader/download"
	"tgloader/models"
)

func main() {
	// Flush log immediately for Docker
	log.SetOutput(os.Stdout)
	log.SetFlags(log.LstdFlags)

	log.Println("=== TGloader Starting ===")
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	log.Println("Loading config...")
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Create directories
	dirs := []string{cfg.Storage.BasePath, cfg.Storage.TempPath, "./cache"}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			log.Printf("Warning: could not create %s: %v", dir, err)
		}
	}

	for _, dirName := range cfg.Storage.Categories {
		dirPath := filepath.Join(cfg.Storage.BasePath, dirName)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			log.Printf("Warning: could not create %s: %v", dirPath, err)
		}
	}

	// Create download queue
	queueSize := cfg.Downloads.ConcurrentLimit * 2
	if queueSize < 5 {
		queueSize = 5
	}
	downloadQueue := make(chan *models.DownloadJob, queueSize)

	// Create download manager
	downloadMgr := download.NewManager()

	// Create bot service
	botService, err := bot.NewService(cfg, downloadMgr, downloadQueue)
	if err != nil {
		log.Fatalf("Failed to create bot service: %v", err)
	}

	// Start download manager
	go func() {
		if err := downloadMgr.Run(ctx); err != nil {
			log.Printf("Download manager error: %v", err)
		}
	}()

	// Submit jobs to download manager
	go func() {
		for job := range downloadQueue {
			downloadMgr.Submit(job)
		}
	}()

	// Run bot (blocks)
	log.Println("Starting TGloader Bot...")
	if err := botService.Run(ctx); err != nil {
		log.Printf("Bot error: %v", err)
	}

	log.Println("Shutdown complete")
}
