package download

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"tgloader/models"
)

type Manager struct {
	jobs       map[string]*models.DownloadJob
	jobsMu     sync.RWMutex
	activeJobs map[string]*activeDownload
	activeMu   sync.RWMutex
	queue      chan *models.DownloadJob
	queuedJobs []*models.DownloadJob
	queuedMu   sync.Mutex
	wg         sync.WaitGroup
	ctx        context.Context
	stats      Stats
	statsMu    sync.Mutex
	stopCh     chan struct{}
	seqMu      sync.Mutex
	seqCounter int
}

type Stats struct {
	Total     int64 `json:"total"`
	Completed int64 `json:"completed"`
	Failed    int64 `json:"failed"`
	Queued    int   `json:"queued"`
	Active    int   `json:"active"`
}

type activeDownload struct {
	job        *models.DownloadJob
	cancelCh   chan struct{}
	lastUpdate time.Time
}

func NewManager() *Manager {
	m := &Manager{
		jobs:       make(map[string]*models.DownloadJob),
		activeJobs: make(map[string]*activeDownload),
		queue:      make(chan *models.DownloadJob, 100),
		queuedJobs: make([]*models.DownloadJob, 0),
		stopCh:     make(chan struct{}),
		stats:      Stats{},
	}
	log.Printf("Download manager initialized")
	return m
}

func (m *Manager) Run(ctx context.Context) error {
	m.ctx = ctx
	concurrent := 5
	if c := os.Getenv("ARIA2_CONCURRENT_DOWNLOADS"); c != "" {
		if parsed, err := fmt.Sscanf(c, "%d", &concurrent); err == nil && parsed > 0 {
			log.Printf("Using custom concurrent limit: %d", concurrent)
		}
	}
	log.Printf("Download manager started with %d workers", concurrent)
	for i := 0; i < concurrent; i++ {
		m.wg.Add(1)
		go m.worker(ctx, i)
	}
	go m.monitorProgress()
	return nil
}

func (m *Manager) Submit(job *models.DownloadJob) {
	m.jobsMu.Lock()
	m.jobs[job.ID] = job
	m.jobsMu.Unlock()
	m.activeMu.Lock()
	m.activeJobs[job.ID] = &activeDownload{
		job:        job,
		cancelCh:   make(chan struct{}),
		lastUpdate: time.Now(),
	}
	m.activeMu.Unlock()
	select {
	case m.queue <- job:
		log.Printf("Job %s sent to worker", job.Filename)
	default:
		m.queuedMu.Lock()
		m.queuedJobs = append(m.queuedJobs, job)
		m.stats.Queued = len(m.queuedJobs)
		m.queuedMu.Unlock()
		log.Printf("Job %s queued (channel full)", job.Filename)
	}
}

func (m *Manager) worker(ctx context.Context, workerID int) {
	defer m.wg.Done()
	log.Printf("Download worker %d started", workerID)
	for {
		select {
		case <-ctx.Done():
			log.Printf("Download worker %d stopping", workerID)
			return
		case job, ok := <-m.queue:
			if !ok {
				log.Printf("Download worker %d: queue closed", workerID)
				return
			}
			m.processJob(ctx, job, workerID)
		}
	}
}

func (m *Manager) processJob(ctx context.Context, job *models.DownloadJob, workerID int) {
	log.Printf("Worker %d processing: %s", workerID, job.Filename)
	now := time.Now()
	job.Status = models.DownloadActive
	job.StartedAt = &now
	m.statsMu.Lock()
	m.stats.Active++
	m.statsMu.Unlock()
	m.queuedMu.Lock()
	for i, j := range m.queuedJobs {
		if j.ID == job.ID {
			m.queuedJobs = append(m.queuedJobs[:i], m.queuedJobs[i+1:]...)
			break
		}
	}
	m.stats.Queued = len(m.queuedJobs)
	m.queuedMu.Unlock()

	// Handle local files
	if job.URL != "" && len(job.URL) > 7 && job.URL[:7] == "file://" {
		m.processLocalFile(job)
		return
	}

	// For jobs with TargetPath, monitor file download progress
	// (download happens in bot.go, worker just tracks)
	if job.TargetPath != "" {
		m.monitorDownloadProgress(ctx, job, workerID)
		return
	}

	job.Status = models.DownloadComplete
	job.Progress = 100
	m.statsMu.Lock()
	m.stats.Total++
	m.stats.Completed++
	m.stats.Active--
	m.statsMu.Unlock()
	m.activeMu.Lock()
	delete(m.activeJobs, job.ID)
	m.activeMu.Unlock()
	log.Printf("Job %s completed", job.Filename)
}

// monitorDownloadProgress tracks download progress from bot.go
func (m *Manager) monitorDownloadProgress(ctx context.Context, job *models.DownloadJob, workerID int) {
	maxWait := 3600 * time.Second // 1 hour timeout for large files
	checkInterval := 2 * time.Second
	startTime := time.Now()
	var lastSize int64
	var lastLogTime time.Time
	downloadDone := false

	for !downloadDone {
		select {
		case <-ctx.Done():
			job.Status = models.DownloadCancelled
			m.statsMu.Lock()
			m.stats.Active--
			m.statsMu.Unlock()
			return
		default:
		}

		info, err := os.Stat(job.TargetPath)
		if err == nil {
			currentSize := info.Size()
			job.DownloadedSize = currentSize

			elapsed := time.Since(startTime).Seconds()
			if elapsed > 0 {
				job.DownloadSpeed = int64(float64(currentSize) / elapsed)
			}

			if job.FileSize > 0 {
				job.Progress = float64(currentSize) / float64(job.FileSize) * 100
				job.TotalSize = job.FileSize
			}

			// Log every 30 seconds
			if time.Since(lastLogTime) >= 30*time.Second && currentSize > 0 {
				log.Printf("Worker %d: %s - %s (%s/s)", workerID, job.Filename, formatBytes(currentSize), formatBytes(job.DownloadSpeed))
				lastLogTime = time.Now()
			}

			// Check if download is complete (file stable)
			if currentSize == lastSize && currentSize > 0 {
				time.Sleep(checkInterval)
				if info2, err2 := os.Stat(job.TargetPath); err2 == nil && info2.Size() == currentSize {
					if job.FileSize > 0 && currentSize < job.FileSize {
						log.Printf("Worker %d: file size %s < expected %s", workerID, formatBytes(currentSize), formatBytes(job.FileSize))
					}
					job.DownloadedSize = currentSize
					job.TotalSize = currentSize
					job.Progress = 100
					job.DownloadSpeed = 0
					downloadDone = true
					break
				}
			}
			lastSize = currentSize
		}

		if time.Since(startTime) > maxWait {
			job.Status = models.DownloadError
			job.ErrorMsg = "timeout waiting for file"
			m.statsMu.Lock()
			m.stats.Total++
			m.stats.Failed++
			m.stats.Active--
			m.statsMu.Unlock()
			log.Printf("Worker %d: timeout for %s", workerID, job.Filename)
			return
		}

		time.Sleep(checkInterval)
	}

	job.Status = models.DownloadComplete
	m.statsMu.Lock()
	m.stats.Total++
	m.stats.Completed++
	m.stats.Active--
	m.statsMu.Unlock()
	m.activeMu.Lock()
	delete(m.activeJobs, job.ID)
	m.activeMu.Unlock()
	log.Printf("Worker %d: completed %s", workerID, job.Filename)
}

func (m *Manager) processLocalFile(job *models.DownloadJob) {
	localPath := job.URL[7:]
	downloadPath := getEnv("DOWNLOAD_PATH", "./downloads")
	categoryPath := filepath.Join(downloadPath, models.GetCategoryFolderName(job.Category))

	if err := os.MkdirAll(categoryPath, 0755); err != nil {
		job.Status = models.DownloadError
		job.ErrorMsg = fmt.Sprintf("create dir: %v", err)
		m.statsMu.Lock()
		m.stats.Total++
		m.stats.Failed++
		m.statsMu.Unlock()
		log.Printf("Job %s failed: %v", job.Filename, err)
		return
	}

	targetPath := filepath.Join(categoryPath, job.Filename)
	sourceData, err := os.ReadFile(localPath)
	if err != nil {
		job.Status = models.DownloadError
		job.ErrorMsg = fmt.Sprintf("read file: %v", err)
		m.statsMu.Lock()
		m.stats.Total++
		m.stats.Failed++
		m.statsMu.Unlock()
		log.Printf("Job %s failed: %v", job.Filename, err)
		return
	}

	if err := os.WriteFile(targetPath, sourceData, 0644); err != nil {
		job.Status = models.DownloadError
		job.ErrorMsg = fmt.Sprintf("write file: %v", err)
		m.statsMu.Lock()
		m.stats.Total++
		m.stats.Failed++
		m.statsMu.Unlock()
		log.Printf("Job %s failed: %v", job.Filename, err)
		return
	}

	job.Status = models.DownloadComplete
	job.DownloadedSize = int64(len(sourceData))
	job.TotalSize = job.DownloadedSize
	job.Progress = 100
	m.statsMu.Lock()
	m.stats.Total++
	m.stats.Completed++
	m.stats.Active--
	m.statsMu.Unlock()
	m.activeMu.Lock()
	delete(m.activeJobs, job.ID)
	m.activeMu.Unlock()
	log.Printf("Job %s completed", job.Filename)
}

func (m *Manager) monitorProgress() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
		}
	}
}

func (m *Manager) GetActiveDownloads() []*models.DownloadJob {
	m.activeMu.RLock()
	defer m.activeMu.RUnlock()
	result := make([]*models.DownloadJob, 0, len(m.activeJobs))
	for _, active := range m.activeJobs {
		result = append(result, active.job)
	}
	return result
}

func (m *Manager) GetQueuedJobs() []*models.DownloadJob {
	m.queuedMu.Lock()
	defer m.queuedMu.Unlock()
	result := make([]*models.DownloadJob, len(m.queuedJobs))
	copy(result, m.queuedJobs)
	return result
}

func (m *Manager) GetStats() Stats {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	return m.stats
}

func (m *Manager) CancelJob(id string) error {
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()
	if job, ok := m.jobs[id]; ok {
		job.Status = models.DownloadCancelled
		return nil
	}
	return fmt.Errorf("job not found: %s", id)
}

func (m *Manager) CancelAll() int {
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()
	count := 0
	for _, job := range m.jobs {
		if job.Status == models.DownloadActive || job.Status == models.DownloadQueued {
			job.Status = models.DownloadCancelled
			count++
		}
	}
	m.queuedMu.Lock()
	m.queuedJobs = nil
	m.queuedMu.Unlock()
	return count
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
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
