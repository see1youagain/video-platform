package logic

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"video-platform/internal/db"
	"video-platform/internal/redis"
)

var (
	ErrUploadAlreadyCompleted = errors.New("upload already completed")
	ErrUploadCancelled        = errors.New("upload was cancelled")
)

// InitUploadResult 初始化上传结果
type InitUploadResult struct {
	ContentID uint
	Status    string // "new", "resumable", "fast_upload"
}

// InitUpload 初始化上传（带墓碑检查和分布式锁）
func InitUpload(ctx context.Context, userID int, fileName, fileHash string) (*InitUploadResult, error) {
	// 1. 先检查 Redis 墓碑
	exists, status, err := redis.CheckTombstone(ctx, userID, fileHash)
	if err != nil {
		// Redis 错误不致命，继续检查数据库
		log.Printf("Warning: check tombstone failed: %v", err)
	}

	if exists && status == "completed" {
		contentID, err := redis.GetTombstoneContentID(ctx, userID, fileHash)
		if err == nil && contentID > 0 {
			return &InitUploadResult{
				ContentID: contentID,
				Status:    "fast_upload",
			}, nil
		}
	}

	if exists && status == "cancelled" {
		_ = redis.DeleteTombstone(ctx, userID, fileHash)
	}

	// 2. 如果 Redis 没有墓碑，再检查数据库（双重保险）
	if !exists {
		if uc, err := db.GetUserContentByHash(ctx, userID, fileHash); err == nil && uc.Status == 1 {
			// 数据库中存在已完成的记录，验证文件是否存在
			if fm, err := db.GetFileMeta(ctx, fileHash); err == nil && fm.FilePath != "" {
				// 文件存在，创建墓碑并返回秒传
				_ = redis.CreateTombstoneNoExpire(ctx, userID, fileHash, uc.ContentID, "completed")
				return &InitUploadResult{
					ContentID: uc.ContentID,
					Status:    "fast_upload",
				}, nil
			}
		}
	}

	// 3. 获取分布式锁
	lockKey := fmt.Sprintf("upload:init:%d:%s", userID, fileHash)
	lock := redis.NewLock(lockKey, 30*time.Second)
	if err := lock.Lock(ctx); err != nil {
		return nil, fmt.Errorf("acquire lock failed: %w", err)
	}
	defer lock.Unlock(ctx)

	// 4. 数据库操作
	contentID, err := db.CreateOrUpdateUserFileUploading(ctx, userID, fileName, fileHash)
	if err != nil {
		return nil, err
	}

	return &InitUploadResult{
		ContentID: contentID,
		Status:    "new",
	}, nil
}

// CheckBeforeUploadChunk 上传分块前检查墓碑
func CheckBeforeUploadChunk(ctx context.Context, userID int, fileHash string) error {
	exists, status, err := redis.CheckTombstone(ctx, userID, fileHash)
	if err != nil {
		return fmt.Errorf("check tombstone failed: %w", err)
	}
	if exists && status == "completed" {
		return ErrUploadAlreadyCompleted
	}
	if exists && status == "cancelled" {
		return ErrUploadCancelled
	}
	return nil
}

// FastUpload 秒传（带分布式锁和墓碑创建）
func FastUpload(ctx context.Context, userID int, contentID uint, fileName, fileHash string) error {
	lockKey := fmt.Sprintf("upload:fast:%d:%s", userID, fileHash)
	lock := redis.NewLock(lockKey, 30*time.Second)
	if err := lock.Lock(ctx); err != nil {
		return fmt.Errorf("acquire lock failed: %w", err)
	}
	defer lock.Unlock(ctx)

	// 数据库操作
	if err := db.CreateUserFileForFastUpload(ctx, userID, contentID, fileName, fileHash); err != nil {
		return err
	}

	// 创建墓碑
	if err := redis.CreateTombstone(ctx, userID, fileHash, contentID, "completed"); err != nil {
		log.Printf("create tombstone failed: %v\n", err)
	}

	return nil
}

// MergeChunks 合并分块（带分布式锁和墓碑创建）
func MergeChunks(ctx context.Context, userID int, contentID uint, fileName, fileHash, filePath string, fileSize int64) error {
	lockKey := fmt.Sprintf("upload:merge:%d:%s", userID, fileHash)
	lock := redis.NewLock(lockKey, 60*time.Second) // 合并可能耗时较长
	if err := lock.Lock(ctx); err != nil {
		return fmt.Errorf("acquire lock failed: %w", err)
	}
	defer lock.Unlock(ctx)

	// 数据库操作
	if err := db.FinishMergeAndCreateMeta(ctx, userID, contentID, fileName, fileHash, filePath, fileSize); err != nil {
		return err
	}

	// 创建墓碑
	if err := redis.CreateTombstone(ctx, userID, fileHash, contentID, "completed"); err != nil {
		log.Printf("create tombstone failed: %v\n", err)
	}

	return nil
}

// CancelUpload 取消上传（带分布式锁和墓碑创建）
func CancelUpload(ctx context.Context, userID int, fileHash string, contentID uint) error {
	lockKey := fmt.Sprintf("upload:cancel:%d:%s", userID, fileHash)
	lock := redis.NewLock(lockKey, 30*time.Second)
	if err := lock.Lock(ctx); err != nil {
		return fmt.Errorf("acquire lock failed: %w", err)
	}
	defer lock.Unlock(ctx)

	// 更新状态
	if err := db.UpdateUserContentStatus(ctx, userID, contentID, -1); err != nil {
		return err
	}

	// 创建取消墓碑
	return redis.CreateTombstone(ctx, userID, fileHash, contentID, "cancelled")
}

// DeleteFile 删除文件（带分布式锁）
func DeleteFile(ctx context.Context, userID int, fileHash string) error {
	lockKey := fmt.Sprintf("file:delete:%d:%s", userID, fileHash)
	lock := redis.NewLock(lockKey, 30*time.Second)
	if err := lock.Lock(ctx); err != nil {
		return fmt.Errorf("acquire lock failed: %w", err)
	}
	defer lock.Unlock(ctx)

	// 数据库操作
	if err := db.DeleteUserFile(ctx, userID, fileHash); err != nil {
		return err
	}

	// 删除墓碑（允许重新上传）
	_ = redis.DeleteTombstone(ctx, userID, fileHash)

	return nil
}

// FileInfo 文件信息（用于返回给前端）
type FileInfo struct {
	ID        uint   `json:"id"`
	FileName  string `json:"file_name"`
	FileHash  string `json:"file_hash"`
	FileSize  int64  `json:"file_size"`
	Status    int    `json:"status"`
	CreatedAt string `json:"created_at"`
}

// ContentInfo 内容信息
type ContentInfo struct {
	ID         uint   `json:"id"`
	Title      string `json:"title"`
	SourceHash string `json:"source_hash"`
	CreatedAt  string `json:"created_at"`
}

// ListUserFiles 列出用户的文件
func ListUserFiles(ctx context.Context, userID int) ([]FileInfo, error) {
	userContents, err := db.GetUserContents(ctx, userID)
	if err != nil {
		return nil, err
	}

	var files []FileInfo
	for _, uc := range userContents {
		// 获取文件大小
		var fileSize int64
		if fm, err := db.GetFileMeta(ctx, uc.FileHash); err == nil {
			fileSize = fm.FileSize
		}

		files = append(files, FileInfo{
			ID:        uc.ID,
			FileName:  uc.FileName,
			FileHash:  uc.FileHash,
			FileSize:  fileSize,
			Status:    uc.Status,
			CreatedAt: uc.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}

	return files, nil
}

// GetUserFile 获取用户的单个文件
func GetUserFile(ctx context.Context, userID int, fileHash string) (*FileInfo, error) {
	uc, err := db.GetUserContentByHash(ctx, userID, fileHash)
	if err != nil {
		return nil, err
	}

	var fileSize int64
	if fm, err := db.GetFileMeta(ctx, fileHash); err == nil {
		fileSize = fm.FileSize
	}

	return &FileInfo{
		ID:        uc.ID,
		FileName:  uc.FileName,
		FileHash:  uc.FileHash,
		FileSize:  fileSize,
		Status:    uc.Status,
		CreatedAt: uc.CreatedAt.Format("2006-01-02 15:04:05"),
	}, nil
}

// ListUserContents 列出用户的内容
func ListUserContents(ctx context.Context, userID int) ([]ContentInfo, error) {
	contents, err := db.GetContentsByOwner(ctx, userID)
	if err != nil {
		return nil, err
	}

	var result []ContentInfo
	for _, c := range contents {
		result = append(result, ContentInfo{
			ID:         c.ID,
			Title:      c.Title,
			SourceHash: c.SourceHash,
			CreatedAt:  c.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}

	return result, nil
}

// GetUserContent 获取用户的单个内容
func GetUserContent(ctx context.Context, userID int, contentID string) (*ContentInfo, error) {
	content, err := db.GetContentByID(ctx, userID, contentID)
	if err != nil {
		return nil, err
	}

	return &ContentInfo{
		ID:         content.ID,
		Title:      content.Title,
		SourceHash: content.SourceHash,
		CreatedAt:  content.CreatedAt.Format("2006-01-02 15:04:05"),
	}, nil
}
