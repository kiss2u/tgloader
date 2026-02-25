package models

import (
	"path/filepath"
	"strings"
)

// GetFileCategory determines the category of a file based on its extension
func GetFileCategory(filename string) FileCategory {
	ext := strings.ToLower(filepath.Ext(filename))

	switch ext {
	case ".pdf", ".doc", ".docx", ".txt", ".rtf", ".odt", ".xls", ".xlsx", ".ppt", ".pptx", ".csv":
		return CategoryDocuments
	case ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz", ".iso", ".dmg":
		return CategoryArchives
	case ".mp4", ".avi", ".mkv", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".3gp", ".mpg", ".mpeg", ".ts":
		return CategoryVideos
	case ".mp3", ".wav", ".flac", ".aac", ".ogg", ".m4a", ".wma", ".opus":
		return CategoryAudio
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".svg", ".webp", ".tiff", ".ico":
		return CategoryImages
	case ".epub", ".mobi", ".azw", ".azw3", ".djvu", ".fb2":
		return CategoryEbooks
	case ".exe", ".msi", ".deb", ".rpm", ".app", ".pkg", ".jar", ".apk":
		return CategorySoftware
	default:
		return CategoryOther
	}
}

// GetCategoryFolderName gets the human-readable folder name for a category
func GetCategoryFolderName(category FileCategory) string {
	switch category {
	case CategoryDocuments:
		return "Documents"
	case CategoryArchives:
		return "Archives"
	case CategoryVideos:
		return "Videos"
	case CategoryAudio:
		return "Audio"
	case CategoryImages:
		return "Images"
	case CategoryEbooks:
		return "Ebooks"
	case CategorySoftware:
		return "Software"
	default:
		return "Other"
	}
}
