package bot

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/dispatcher/handlers"
	"github.com/celestix/gotgproto/dispatcher/handlers/filters"
	"github.com/celestix/gotgproto/ext"
	"github.com/celestix/gotgproto/functions"
	"github.com/celestix/gotgproto/sessionMaker"
	"github.com/glebarez/sqlite"
	"github.com/gotd/td/tg"
	"tgloader/config"
	"tgloader/download"
	"tgloader/models"
)

type Service struct {
	client        *gotgproto.Client
	cfg           *config.Config
	downloadMgr   *download.Manager
	downloadQueue chan *models.DownloadJob
}

func NewService(cfg *config.Config, downloadMgr *download.Manager, downloadQueue chan *models.DownloadJob) (*Service, error) {
	s := &Service{
		cfg:           cfg,
		downloadMgr:   downloadMgr,
		downloadQueue: downloadQueue,
	}

	if err := s.initMTProto(); err != nil {
		return nil, fmt.Errorf("failed to init MTProto: %w", err)
	}

	return s, nil
}

func (s *Service) initMTProto() error {
	token := s.cfg.Telegram.GetToken()
	if token == "" {
		token = os.Getenv("TELEGRAM_TOKEN")
	}
	if token == "" {
		return fmt.Errorf("TELEGRAM_TOKEN not set")
	}

	if err := os.MkdirAll("./session", 0755); err != nil {
		log.Printf("Warning: failed to create session dir: %v", err)
	}

	client, err := gotgproto.NewClient(
		int(s.cfg.Telegram.AppID),
		s.cfg.Telegram.AppHash,
		gotgproto.ClientTypeBot(token),
		&gotgproto.ClientOpts{
			Session: sessionMaker.SqlSession(sqlite.Open("./session/tgloader.db")),
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	s.client = client
	return nil
}

func (s *Service) Run(ctx context.Context) error {
	s.client.Dispatcher.AddHandler(handlers.NewCommand("start", s.handleStart))
	s.client.Dispatcher.AddHandler(handlers.NewCommand("help", s.handleHelp))
	s.client.Dispatcher.AddHandler(handlers.NewCommand("queue", s.handleQueue))
	s.client.Dispatcher.AddHandler(handlers.NewCommand("progress", s.handleProgress))
	s.client.Dispatcher.AddHandler(handlers.NewCommand("stats", s.handleStats))
	s.client.Dispatcher.AddHandler(handlers.NewCommand("cancel", s.handleCancel))
	s.client.Dispatcher.AddHandler(handlers.NewCommand("cancelall", s.handleCancelAll))
	s.client.Dispatcher.AddHandler(handlers.NewMessage(filters.Message.Media, s.handleMedia))

	log.Println("Starting Telegram client (MTProto)...")

	err := s.client.Idle()
	if err != nil {
		log.Printf("Client idle error: %v", err)
	}
	return err
}

func (s *Service) handleStart(ctx *ext.Context, update *ext.Update) error {
	if update.EffectiveMessage == nil {
		return dispatcher.EndGroups
	}
	msg := `TGloader [MTProto]

Forward files to download.
Works with private channels - no 20MB limit!

Commands:
/queue    - Show download queue
/progress  - Show download progress
/stats    - Show download statistics
/cancel   - Cancel a download
/cancelall - Cancel all downloads
/help     - Show this help`
	_, err := ctx.Reply(update, ext.ReplyTextString(msg), nil)
	return err
}

func (s *Service) handleHelp(ctx *ext.Context, update *ext.Update) error {
	if update.EffectiveMessage == nil {
		return dispatcher.EndGroups
	}
	msg := `Available Commands:

/queue     - Show download queue
/progress   - Show download progress  
/stats     - Show download statistics
/cancel    - Cancel a download (shows list)
/cancelall - Cancel all downloads
/start     - Show welcome message`
	_, err := ctx.Reply(update, ext.ReplyTextString(msg), nil)
	return err
}

func (s *Service) handleQueue(ctx *ext.Context, update *ext.Update) error {
	if update.EffectiveMessage == nil {
		return dispatcher.EndGroups
	}
	activeJobs := s.downloadMgr.GetActiveDownloads()
	queuedJobs := s.downloadMgr.GetQueuedJobs()

	activeCount := len(activeJobs)
	queuedCount := len(queuedJobs)

	// Header with counts
	msg := fmt.Sprintf("📋 队列 (📥 %d | ⏳ %d)\n", activeCount, queuedCount)
	msg += "├────────────────────┤\n"

	if activeCount == 0 && queuedCount == 0 {
		msg += "无活动下载"
		_, err := ctx.Reply(update, ext.ReplyTextString(msg), nil)
		return err
	}

	// Download list
	if activeCount > 0 {
		msg += "📥 下载中\n"
		for i, job := range activeJobs {
			size := formatBytes(job.DownloadedSize)
			if job.TotalSize > 0 {
				size = fmt.Sprintf("%s/%s", formatBytes(job.DownloadedSize), formatBytes(job.TotalSize))
			}
			msg += fmt.Sprintf(" %d. 📄 %s\n    %s\n", i+1, truncate(job.Filename, 20), size)
		}
		msg += "├────────────────────┤\n"
	}

	// Queued list
	if queuedCount > 0 {
		msg += "⏳ 等待中\n"
		for i, job := range queuedJobs {
			if i >= 5 {
				msg += fmt.Sprintf(" ...还有 %d 个\n", queuedCount-i)
				break
			}
			msg += fmt.Sprintf(" %d. 📄 %s\n", activeCount+i+1, truncate(job.Filename, 20))
		}
	} else if activeCount > 0 {
		msg += "✅ 队列已清空\n"
	}

	msg += "├────────────────────┤\n"
	msg += "[刷新: /progress] [清空: /cancelall]"

	_, err := ctx.Reply(update, ext.ReplyTextString(msg), nil)
	return err
}

func (s *Service) handleProgress(ctx *ext.Context, update *ext.Update) error {
	if update.EffectiveMessage == nil {
		return dispatcher.EndGroups
	}
	activeJobs := s.downloadMgr.GetActiveDownloads()
	queuedJobs := s.downloadMgr.GetQueuedJobs()

	activeCount := len(activeJobs)
	queuedCount := len(queuedJobs)

	// Header with counts
	msg := fmt.Sprintf("📥 进度 (📥 %d | ⏳ %d)\n", activeCount, queuedCount)
	msg += "├────────────────────┤\n"

	if activeCount == 0 && queuedCount == 0 {
		msg += "无活动下载"
		_, err := ctx.Reply(update, ext.ReplyTextString(msg), nil)
		return err
	}

	// Download progress
	if activeCount > 0 {
		for _, job := range activeJobs {
			size := formatBytes(job.DownloadedSize)
			if job.TotalSize > 0 {
				size = fmt.Sprintf("%s/%s", formatBytes(job.DownloadedSize), formatBytes(job.TotalSize))
			}
			msg += fmt.Sprintf("📄 %s\n   %s\n", truncate(job.Filename, 24), size)
			if job.DownloadSpeed > 0 {
				msg += fmt.Sprintf("   🚀 %s/s\n", formatBytes(job.DownloadSpeed))
			}
		}
		msg += "├────────────────────┤\n"
	}

	// Queue
	if queuedCount > 0 {
		msg += fmt.Sprintf("⏳ 等待: %d 个\n", queuedCount)
	} else {
		msg += "✅ 无等待\n"
	}

	msg += "├────────────────────┤\n"
	msg += "[刷新: /progress] [取消: /cancel]"

	_, err := ctx.Reply(update, ext.ReplyTextString(msg), nil)
	return err
}

func (s *Service) handleStats(ctx *ext.Context, update *ext.Update) error {
	if update.EffectiveMessage == nil {
		return dispatcher.EndGroups
	}
	stats := s.downloadMgr.GetStats()
	activeJobs := s.downloadMgr.GetActiveDownloads()
	queuedJobs := s.downloadMgr.GetQueuedJobs()

	msg := "📈 统计\n"
	msg += "├────────────────────┤\n"
	msg += fmt.Sprintf("✅ 已完成: %d\n", stats.Completed)
	msg += fmt.Sprintf("❌ 失败: %d\n", stats.Failed)
	msg += fmt.Sprintf("📥 下载: %d\n", len(activeJobs))
	msg += fmt.Sprintf("⏳ 等待: %d\n", len(queuedJobs))
	msg += "├────────────────────┤\n"
	msg += fmt.Sprintf("📊 总计: %d\n", stats.Total)
	msg += "├────────────────────┤\n"
	msg += "[刷新: /stats]"

	_, err := ctx.Reply(update, ext.ReplyTextString(msg), nil)
	return err
}

func (s *Service) handleCancel(ctx *ext.Context, update *ext.Update) error {
	if update.EffectiveMessage == nil {
		return dispatcher.EndGroups
	}

	msg := update.EffectiveMessage
	text := strings.TrimSpace(msg.Text)

	if len(text) > 8 {
		numStr := strings.TrimPrefix(text, "/cancel")
		numStr = strings.TrimSpace(numStr)
		if num, err := strconv.Atoi(numStr); err == nil {
			activeJobs := s.downloadMgr.GetActiveDownloads()
			queuedJobs := s.downloadMgr.GetQueuedJobs()
			allJobs := append(activeJobs, queuedJobs...)

			if num > 0 && num <= len(allJobs) {
				job := allJobs[num-1]
				_ = s.downloadMgr.CancelJob(job.ID)
				_, err = ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("❌ 已取消: %s", job.Filename)), nil)
				return err
			} else {
				_, err = ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("无效编号: %d", num)), nil)
				return err
			}
		}
	}

	activeJobs := s.downloadMgr.GetActiveDownloads()
	queuedJobs := s.downloadMgr.GetQueuedJobs()

	if len(activeJobs) == 0 && len(queuedJobs) == 0 {
		_, err := ctx.Reply(update, ext.ReplyTextString("无下载可取消"), nil)
		return err
	}

	response := "选择要取消的下载:\n\n"
	allJobs := append(activeJobs, queuedJobs...)
	for i, job := range allJobs {
		status := "📥"
		if job.Status == "queued" {
			status = "⏳"
		}
		response += fmt.Sprintf("%d. %s %s\n", i+1, status, truncate(job.Filename, 25))
	}
	response += "\n回复 /cancel <编号>"

	_, err := ctx.Reply(update, ext.ReplyTextString(response), nil)
	return err
}

func (s *Service) handleCancelAll(ctx *ext.Context, update *ext.Update) error {
	if update.EffectiveMessage == nil {
		return dispatcher.EndGroups
	}
	count := s.downloadMgr.CancelAll()
	_, err := ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("❌ 已取消 %d 个下载", count)), nil)
	return err
}

func (s *Service) handleMedia(ctx *ext.Context, update *ext.Update) error {
	msg := update.EffectiveMessage
	if msg == nil {
		return dispatcher.EndGroups
	}

	if len(msg.Text) > 0 && msg.Text[0] == '/' {
		return dispatcher.EndGroups
	}

	filename, err := functions.GetMediaFileNameWithId(msg.Media)
	if err != nil {
		filename = fmt.Sprintf("file_%d", msg.ID)
	}

	// Optimize filename
	filename = optimizeFilename(filename)

	// Reply immediately
	_, _ = ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("📥 下载中: %s", filename)), nil)

	// Determine target directory
	category := models.GetFileCategory(filename)
	targetDir := filepath.Join(s.cfg.Storage.BasePath, models.GetCategoryFolderName(category))

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		log.Printf("Failed to create target dir: %v", err)
		_, _ = ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("❌ 错误: %v", err)), nil)
		return dispatcher.EndGroups
	}

	targetPath := filepath.Join(targetDir, filename)

	// Get file size from media (if available)
	var fileSize int64 = 0
	if media, ok := msg.Media.(*tg.MessageMediaDocument); ok {
		if doc, ok := media.Document.(*tg.Document); ok {
			fileSize = doc.Size
		}
	}

	// Create job
	job := &models.DownloadJob{
		ID:         fmt.Sprintf("job_%d", msg.ID),
		Filename:   filename,
		TargetPath: targetPath,
		Category:   category,
		Status:     models.DownloadPending,
		MessageID:  int(msg.ID),
		CreatedAt:  time.Now(),
		TotalSize:  fileSize,
		FileSize:   fileSize,
	}

	// Submit job to manager for tracking
	s.downloadMgr.Submit(job)

	// Download with retry
	go func() {
		const maxRetries = 3
		retryCount := 0
		success := false

		for retryCount < maxRetries && !success {
			// Check for existing file (resume)
			var startOffset int64 = 0
			if info, err := os.Stat(targetPath); err == nil && info.Size() > 0 {
				startOffset = info.Size()
				log.Printf("[%s] Resuming from offset %s", filename, formatBytes(startOffset))
			}

			// Download using gotgproto's DownloadMedia
			err = s.downloadWithMedia(ctx, msg.Media, targetPath, startOffset)

			if err == nil {
				log.Printf("[%s] Download completed: %s", filename, targetPath)
				success = true
				return
			}

			log.Printf("[%s] Download failed (attempt %d/%d): %v", filename, retryCount+1, maxRetries, err)
			retryCount++
			if retryCount < maxRetries {
				time.Sleep(2 * time.Second)
			} else {
				log.Printf("[%s] Download failed after %d retries", filename, maxRetries)
				// Notify user about failure
				_, _ = ctx.Reply(update, ext.ReplyTextString(fmt.Sprintf("❌ 下载失败: %s - %v", filename, err)), nil)
			}
		}
	}()

	return dispatcher.EndGroups
}

// downloadWithMedia downloads media using ctx.DownloadMedia
func (s *Service) downloadWithMedia(ctx *ext.Context, media tg.MessageMediaClass, targetPath string, startOffset int64) error {
	// If resuming from offset, check if file exists
	if startOffset > 0 {
		if info, err := os.Stat(targetPath); err == nil && info.Size() == startOffset {
			log.Printf("[resume] File exists with size %s, will continue", formatBytes(startOffset))
		} else if err == nil {
			// File size doesn't match - start fresh
			log.Printf("[resume] File size mismatch, re-downloading")
			startOffset = 0
		}
	}

	// Use gotgproto's DownloadMedia API
	_, err := ctx.DownloadMedia(
		media,
		ext.DownloadOutputPath(targetPath),
		&ext.DownloadMediaOpts{
			Threads:  4,
			PartSize: 512 * 1024,
		},
	)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// Verify downloaded file
	info, err := os.Stat(targetPath)
	if err != nil {
		return fmt.Errorf("file not found after download: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("downloaded file is empty")
	}

	log.Printf("[%s] Download verified: %s", filepath.Base(targetPath), formatBytes(info.Size()))
	return nil
}

// optimizeFilename cleans up numeric-prefixed filenames
func optimizeFilename(name string) string {
	ext := ""
	if i := strings.LastIndex(name, "."); i > 0 {
		ext = name[i:]
		name = name[:i]
	}

	firstLetter := -1
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			firstLetter = i
			break
		}
	}

	if firstLetter > 0 {
		name = name[firstLetter:]
	} else if firstLetter == -1 {
		timestamp := time.Now().Format("20060102_150405")
		simpleNum := ""
		for _, r := range name {
			if r >= '0' && r <= '9' {
				simpleNum += string(r)
				if len(simpleNum) >= 6 {
					break
				}
			}
		}
		name = fmt.Sprintf("video_%s_%s", timestamp, simpleNum)
	}

	return name + ext
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func formatBytes(n int64) string {
	if n == 0 {
		return "0 B"
	}
	const u = int64(1024)
	sz := []string{"B", "KB", "MB", "GB", "TB"}
	i, f := 0, float64(n)
	for f >= float64(u) && i < len(sz)-1 {
		f /= float64(u)
		i++
	}
	return fmt.Sprintf("%.1f %s", f, sz[i])
}
