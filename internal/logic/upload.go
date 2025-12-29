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
	// 1. 检查墓碑，防止重复上传已完成的文件
	exists, status, err := redis.CheckTombstone(ctx, userID, fileHash)
	if err != nil {
		return nil, fmt.Errorf("check tombstone failed: %w", err)
	}
	if exists {
		switch status {
		case "completed":
			// 秒传：返回已存在的 content ID
			contentID, err := redis.GetTombstoneContentID(ctx, userID, fileHash)
			if err == nil && contentID > 0 {
				return &InitUploadResult{
					ContentID: contentID,
					Status:    "fast_upload",
				}, nil
			}
		case "cancelled":
			// 允许重新上传，删除旧墓碑
			_ = redis.DeleteTombstone(ctx, userID, fileHash)
		}
	}

	// 2. 获取分布式锁
	lockKey := fmt.Sprintf("upload:init:%d:%s", userID, fileHash)
	lock := redis.NewLock(lockKey, 30*time.Second)
	if err := lock.Lock(ctx); err != nil {
		return nil, fmt.Errorf("acquire lock failed: %w", err)
	}
	defer lock.Unlock(ctx)

	// 3. 数据库操作
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
