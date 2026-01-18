package logic

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"video-platform/internal/db"
)

// DownloadResult 下载结果
type DownloadResult struct {
	Reader      io.ReadCloser
	FileSize    int64
	FileName    string
	ContentType string
	IsRange     bool
	RangeStart  int64
	RangeEnd    int64
	RangeLength int64
}

// DownloadFile 下载文件
func DownloadFile(ctx context.Context, userID int, fileHash string, rangeHeader string) (*DownloadResult, error) {
	// 1. 验证用户权限
	uc, err := db.GetUserContentByHash(ctx, userID, fileHash)
	if err != nil {
		return nil, fmt.Errorf("file not found or access denied")
	}

	// 2. 获取文件元数据
	fm, err := db.GetFileMeta(ctx, fileHash)
	if err != nil {
		return nil, fmt.Errorf("file metadata not found")
	}

	// 3. 检查文件是否存在
	if !Store.FileExists(fileHash) {
		return nil, fmt.Errorf("file not found on storage")
	}

	result := &DownloadResult{
		FileName:    uc.FileName,
		FileSize:    fm.FileSize,
		ContentType: getContentType(uc.FileName),
	}

	// 4. 处理 Range 请求
	if rangeHeader != "" {
		start, end, err := parseRangeHeader(rangeHeader, fm.FileSize)
		if err != nil {
			return nil, fmt.Errorf("invalid range: %w", err)
		}

		reader, err := Store.GetFileRange(fileHash, start, end)
		if err != nil {
			return nil, fmt.Errorf("get file range failed: %w", err)
		}

		result.Reader = reader
		result.IsRange = true
		result.RangeStart = start
		result.RangeEnd = end
		result.RangeLength = end - start + 1
		return result, nil
	}

	// 5. 普通下载
	reader, size, err := Store.GetFile(fileHash)
	if err != nil {
		return nil, fmt.Errorf("get file failed: %w", err)
	}

	result.Reader = reader
	result.FileSize = size
	return result, nil
}

func parseRangeHeader(rangeHeader string, fileSize int64) (start, end int64, err error) {
	if !strings.HasPrefix(rangeHeader, "bytes=") {
		return 0, 0, fmt.Errorf("invalid range format")
	}

	rangeSpec := strings.TrimPrefix(rangeHeader, "bytes=")
	parts := strings.Split(rangeSpec, "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid range format")
	}

	if parts[0] == "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range suffix")
		}
		start = fileSize - suffix
		end = fileSize - 1
	} else if parts[1] == "" {
		start, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range start")
		}
		end = fileSize - 1
	} else {
		start, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range start")
		}
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid range end")
		}
	}

	if start < 0 {
		start = 0
	}
	if end >= fileSize {
		end = fileSize - 1
	}
	if start > end {
		return 0, 0, fmt.Errorf("invalid range: start > end")
	}

	return start, end, nil
}

func getContentType(fileName string) string {
	ext := strings.ToLower(fileName)
	if idx := strings.LastIndex(ext, "."); idx != -1 {
		ext = ext[idx:]
	}

	switch ext {
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".ts":
		return "video/mp2t"
	case ".mp3":
		return "audio/mpeg"
	default:
		return "application/octet-stream"
	}
}
