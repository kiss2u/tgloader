package models

import (
	"time"
)

// FileType represents different types of downloadable content
type FileType string

const (
	FileTypeDocument  FileType = "document"
	FileTypeVideo     FileType = "video"
	FileTypeAudio     FileType = "audio"
	FileTypePhoto     FileType = "photo"
	FileTypeSticker   FileType = "sticker"
	FileTypeVoice     FileType = "voice"
	FileTypeVideoNote FileType = "video_note"
	FileTypeAnimation FileType = "animation"
	FileTypeTorrent   FileType = "torrent"
	FileTypeUnknown   FileType = "unknown"
)

// FileCategory represents how files are organized in storage
type FileCategory string

const (
	CategoryDocuments FileCategory = "documents"
	CategoryArchives  FileCategory = "archives"
	CategoryVideos    FileCategory = "videos"
	CategoryAudio     FileCategory = "audio"
	CategoryImages    FileCategory = "images"
	CategoryEbooks    FileCategory = "ebooks"
	CategorySoftware  FileCategory = "software"
	CategoryOther     FileCategory = "other"
)

// DownloadStatus represents the current state of a download job
type DownloadStatus string

const (
	DownloadPending   DownloadStatus = "pending"
	DownloadQueued    DownloadStatus = "queued"
	DownloadActive    DownloadStatus = "active"
	DownloadPaused    DownloadStatus = "paused"
	DownloadComplete  DownloadStatus = "complete"
	DownloadError     DownloadStatus = "error"
	DownloadCancelled DownloadStatus = "cancelled"
)

// DownloadJob represents a single download task
type DownloadJob struct {
	ID             string         `json:"id"`
	URL            string         `json:"url"`
	TargetPath     string         `json:"target_path"`
	Filename       string         `json:"filename"`
	Category       FileCategory   `json:"category"`
	Status         DownloadStatus `json:"status"`
	Progress       float64        `json:"progress"`
	ErrorMsg       string         `json:"error_msg,omitempty"`
	DownloadType   FileType       `json:"download_type"`
	TotalSize      int64          `json:"total_size"`
	DownloadedSize int64          `json:"downloaded_size"`
	DownloadSpeed  int64          `json:"download_speed"`
	CreatedAt      time.Time      `json:"created_at"`
	StartedAt      *time.Time     `json:"started_at,omitempty"`
	CompletedAt    *time.Time     `json:"completed_at,omitempty"`
	ChatID         int64          `json:"chat_id"`
	MessageID      int            `json:"message_id"`
	ETA            int            `json:"eta"` // ETA in seconds
	// Telegram-specific download info
	FileID     string `json:"file_id"`
	AccessHash int64  `json:"access_hash"`
	DCID       int    `json:"dc_id"`
	FileSize   int64  `json:"file_size"`
}


// FileMetadata represents metadata about a downloaded file
type FileMetadata struct {
	OriginalName string       `json:"original_name"`
	SavedName    string       `json:"saved_name"`
	Category     FileCategory `json:"category"`
	Size         int64        `json:"size"`
	URL          string       `json:"url,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	Source       string       `json:"source"`
	MD5Hash      string       `json:"md5_hash,omitempty"`
	SHA256Hash   string       `json:"sha256_hash,omitempty"`
}
