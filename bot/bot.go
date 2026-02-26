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
	log.Println("Creating bot service...")
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
		log.Println("ERROR: Token is empty!")
		return fmt.Errorf("TELEGRAM_TOKEN not set")
	}
	if len(token) > 20 {
		log.Printf("Init MTProto with token: %s...", token[:20])
	} else {
		log.Printf("Init MTProto with token: %s", token)
	}

	if err := os.MkdirAll("./session", 0755); err != nil {
		log.Printf("Warning: failed to create session dir: %v", err)
	}

	log.Println("Creating MTProto client...")
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

// isAllowed checks if user is allowed to use the bot
func (s *Service) isAllowed(update *ext.Update) bool {
	if len(s.cfg.Telegram.AllowedChatIDs) == 0 {
		return true // No restriction if empty
	}

	msg := update.EffectiveMessage
	if msg == nil {
		return false
	}

	// Get user ID from PeerID
	var userID int64 = 0
	if msg.PeerID != nil {
		switch peer := msg.PeerID.(type) {
		case *tg.PeerUser:
			userID = peer.UserID
		case *tg.PeerChat:
			userID = -peer.ChatID
		case *tg.PeerChannel:
			userID = -peer.ChannelID
		}
	}

	for _, allowedID := range s.cfg.Telegram.AllowedChatIDs {
		if userID == allowedID {
			return true
		}
	}

	log.Printf("Unauthorized access attempt from user ID: %d", userID)
	return false
}

// isAuthorized replies with error if user not allowed
func (s *Service) isAuthorized(ctx *ext.Context, update *ext.Update) bool {
	if !s.isAllowed(update) {
		_, _ = ctx.Reply(update, ext.ReplyTextString("❌ 未授权用户"), nil)
		return false
	}
	return true
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

// Mobile-friendly response helper
func mobileReply(ctx *ext.Context, update *ext.Update, text string) {
	_, _ = ctx.Reply(update, ext.ReplyTextString(text), nil)
}

func (s *Service) handleStart(ctx *ext.Context, update *ext.Update) error {
	if update.EffectiveMessage == nil {
		return dispatcher.EndGroups
	}
	if !s.isAuthorized(ctx, update) {
		return dispatcher.EndGroups
	}

	msg := `TGloader 📥

转发文件自动下载
支持私有频道，无20MB限制！

命令：
/queue    队列
/progress 进度
/stats    统计
/cancel   取消
/help     帮助`

	mobileReply(ctx, update, msg)
	return nil
}

func (s *Service) handleHelp(ctx *ext.Context, update *ext.Update) error {
	if update.EffectiveMessage == nil {
		return dispatcher.EndGroups
	}
	if !s.isAuthorized(ctx, update) {
		return dispatcher.EndGroups
	}

	msg := `命令帮助：

/queue   下载队列
/pro 查看gress 查看下载进度
/stats    下载统计
/cancel   取消下载
/cancelall 取消全部
/start    欢迎信息`

	mobileReply(ctx, update, msg)
	return nil
}

func (s *Service) handleQueue(ctx *ext.Context, update *ext.Update) error {
	if update.EffectiveMessage == nil {
		return dispatcher.EndGroups
	}
	if !s.isAuthorized(ctx, update) {
		return dispatcher.EndGroups
	}

	activeJobs := s.downloadMgr.GetActiveDownloads()
	queuedJobs := s.downloadMgr.GetQueuedJobs()

	activeCount := len(activeJobs)
	queuedCount := len(queuedJobs)

	// Mobile-friendly: single line summary first
	msg := fmt.Sprintf("📋 队列: 📥%d | ⏳%d\n", activeCount, queuedCount)

	if activeCount == 0 && queuedCount == 0 {
		msg += "无活动下载"
		mobileReply(ctx, update, msg)
		return nil
	}

	// Show active downloads (mobile: max 3)
	if activeCount > 0 {
		msg += "📥 下载中:\n"
		for i, job := range activeJobs {
			if i >= 3 {
				msg += fmt.Sprintf("...还有 %d 个\n", activeCount-i)
				break
			}
			size := formatBytes(job.DownloadedSize)
			if job.TotalSize > 0 {
				size = fmt.Sprintf("%s/%s", formatBytes(job.DownloadedSize), formatBytes(job.TotalSize))
			}
			msg += fmt.Sprintf("• %s\n  %s\n", truncate(job.Filename, 20), size)
		}
	}

	// Show queued (mobile: max 2)
	if queuedCount > 0 {
		msg += "⏳ 等待:\n"
		for i, job := range queuedJobs {
			if i >= 2 {
				msg += fmt.Sprintf("...还有 %d 个\n", queuedCount-i)
				break
			}
			msg += fmt.Sprintf("• %s\n", truncate(job.Filename, 24))
		}
	}

	msg += "\n/ progress | / cancel"
	mobileReply(ctx, update, msg)
	return nil
}

func (s *Service) handleProgress(ctx *ext.Context, update *ext.Update) error {
	if update.EffectiveMessage == nil {
		return dispatcher.EndGroups
	}
	if !s.isAuthorized(ctx, update) {
		return dispatcher.EndGroups
	}

	activeJobs := s.downloadMgr.GetActiveDownloads()
	queuedJobs := s.downloadMgr.GetQueuedJobs()

	activeCount := len(activeJobs)
	queuedCount := len(queuedJobs)

	msg := fmt.Sprintf("📥 进度: 📥%d | ⏳%d\n", activeCount, queuedCount)

	if activeCount == 0 && queuedCount == 0 {
		msg += "无活动下载"
		mobileReply(ctx, update, msg)
		return nil
	}

	// Show progress (mobile: max 3)
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
	}

	if queuedCount > 0 {
		msg += fmt.Sprintf("⏳ 等待: %d 个\n", queuedCount)
	} else {
		msg += "✅ 无等待\n"
	}

	msg += "\n/ progress | / cancel"
	mobileReply(ctx, update, msg)
	return nil
}

func (s *Service) handleStats(ctx *ext.Context, update *ext.Update) error {
	if update.EffectiveMessage == nil {
		return dispatcher.EndGroups
	}
	if !s.isAuthorized(ctx, update) {
		return dispatcher.EndGroups
	}

	stats := s.downloadMgr.GetStats()
	activeJobs := s.downloadMgr.GetActiveDownloads()
	queuedJobs := s.downloadMgr.GetQueuedJobs()

	// Mobile-friendly: compact stats
	msg := "📈 统计\n"
	msg += fmt.Sprintf("✅ 完成: %d\n", stats.Completed)
	msg += fmt.Sprintf("❌ 失败: %d\n", stats.Failed)
	msg += fmt.Sprintf("📥 活跃: %d\n", len(activeJobs))
	msg += fmt.Sprintf("⏳ 等待: %d\n", len(queuedJobs))
	msg += fmt.Sprintf("📊 总计: %d", stats.Total)

	mobileReply(ctx, update, msg)
	return nil
}

func (s *Service) handleCancel(ctx *ext.Context, update *ext.Update) error {
	if update.EffectiveMessage == nil {
		return dispatcher.EndGroups
	}
	if !s.isAuthorized(ctx, update) {
		return dispatcher.EndGroups
	}

	msg := update.EffectiveMessage
	text := strings.TrimSpace(msg.Text)

	// Parse number from command
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
				mobileReply(ctx, update, fmt.Sprintf("❌ 已取消: %s", truncate(job.Filename, 30)))
				return nil
			} else {
				mobileReply(ctx, update, fmt.Sprintf("无效编号: %d", num))
				return nil
			}
		}
	}

	activeJobs := s.downloadMgr.GetActiveDownloads()
	queuedJobs := s.downloadMgr.GetQueuedJobs()

	if len(activeJobs) == 0 && len(queuedJobs) == 0 {
		mobileReply(ctx, update, "无下载可取消")
		return nil
	}

	// Show list (mobile: max 5)
	response := "选择取消:\n\n"
	allJobs := append(activeJobs, queuedJobs...)
	for i, job := range allJobs {
		if i >= 5 {
			response += fmt.Sprintf("...还有 %d 个", len(allJobs)-i)
			break
		}
		status := "📥"
		if job.Status == "queued" {
			status = "⏳"
		}
		response += fmt.Sprintf("%d. %s %s\n", i+1, status, truncate(job.Filename, 20))
	}
	response += "\n/cancel 编号"

	mobileReply(ctx, update, response)
	return nil
}

func (s *Service) handleCancelAll(ctx *ext.Context, update *ext.Update) error {
	if update.EffectiveMessage == nil {
		return dispatcher.EndGroups
	}
	if !s.isAuthorized(ctx, update) {
		return dispatcher.EndGroups
	}

	count := s.downloadMgr.CancelAll()
	mobileReply(ctx, update, fmt.Sprintf("❌ 已取消 %d 个下载", count))
	return nil
}

func (s *Service) handleMedia(ctx *ext.Context, update *ext.Update) error {
	msg := update.EffectiveMessage
	if msg == nil {
		return dispatcher.EndGroups
	}

	// Check authorization before processing
	if !s.isAuthorized(ctx, update) {
		return dispatcher.EndGroups
	}

	// Skip commands
	if len(msg.Text) > 0 && msg.Text[0] == '/' {
		return dispatcher.EndGroups
	}

	filename, err := functions.GetMediaFileNameWithId(msg.Media)
	if err != nil {
		filename = fmt.Sprintf("file_%d", msg.ID)
	}

	filename = optimizeFilename(filename)

	// Quick acknowledgment (mobile-friendly)
	mobileReply(ctx, update, fmt.Sprintf("📥 下载中: %s", truncate(filename, 30)))

	category := models.GetFileCategory(filename)
	targetDir := filepath.Join(s.cfg.Storage.BasePath, models.GetCategoryFolderName(category))

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		log.Printf("Failed to create target dir: %v", err)
		mobileReply(ctx, update, fmt.Sprintf("❌ 错误: %v", err))
		return dispatcher.EndGroups
	}

	targetPath := filepath.Join(targetDir, filename)

	var fileSize int64 = 0
	if media, ok := msg.Media.(*tg.MessageMediaDocument); ok {
		if doc, ok := media.Document.(*tg.Document); ok {
			fileSize = doc.Size
		}
	}

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

	s.downloadMgr.Submit(job)

	// Pass context to goroutine properly
	go func(jobCtx *ext.Context, jobUpdate *ext.Update, jobFilename string, jobMedia tg.MessageMediaClass, jobTargetPath string) {
		const maxRetries = 3
		retryCount := 0
		success := false

		for retryCount < maxRetries && !success {
			var startOffset int64 = 0
			if info, err := os.Stat(jobTargetPath); err == nil && info.Size() > 0 {
				startOffset = info.Size()
				log.Printf("[%s] Resuming from offset %s", jobFilename, formatBytes(startOffset))
			}

			err = s.downloadWithMedia(jobCtx, jobMedia, jobTargetPath, startOffset)

			if err == nil {
				log.Printf("[%s] Download completed: %s", jobFilename, jobTargetPath)
				success = true
				return
			}

			log.Printf("[%s] Download failed (attempt %d/%d): %v", jobFilename, retryCount+1, maxRetries, err)
			retryCount++
			if retryCount < maxRetries {
				time.Sleep(2 * time.Second)
			} else {
				log.Printf("[%s] Download failed after %d retries", jobFilename, maxRetries)
				_, _ = jobCtx.Reply(jobUpdate, ext.ReplyTextString(fmt.Sprintf("❌ 下载失败: %s - %v", truncate(jobFilename, 20), err)), nil)
			}
		}
	}(ctx, update, filename, msg.Media, targetPath)

	return dispatcher.EndGroups
}

func (s *Service) downloadWithMedia(ctx *ext.Context, media tg.MessageMediaClass, targetPath string, startOffset int64) error {
	if startOffset > 0 {
		if info, err := os.Stat(targetPath); err == nil && info.Size() == startOffset {
			log.Printf("[resume] File exists with size %s, will continue", formatBytes(startOffset))
		} else if err == nil {
			log.Printf("[resume] File size mismatch, re-downloading")
			startOffset = 0
		}
	}

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
