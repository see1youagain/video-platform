package logic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"time"

	"video-platform/internal/db"
	"video-platform/internal/redis"
	"video-platform/internal/store"
)

var (
	ErrUploadAlreadyCompleted = errors.New("upload already completed")
	ErrUploadCancelled        = errors.New("upload was cancelled")
	ErrChunkAlreadyUploaded   = errors.New("chunk already uploaded")
)

// Store 全局存储实例
var Store store.Uploader

// InitStore 初始化存储
func InitStore(basePath, tempPath string) {
	Store = store.NewLocalStore(basePath, tempPath)
}

// InitUploadResult 初始化上传结果
type InitUploadResult struct {
	ContentID      uint   `json:"content_id"`
	Status         string `json:"status"`
	UploadedChunks []int  `json:"uploaded_chunks,omitempty"`
}

// InitUpload 初始化上传
func InitUpload(ctx context.Context, userID int, fileName, fileHash string) (*InitUploadResult, error) {
	// 1. 检查墓碑（秒传检查）
	exists, status, err := redis.CheckTombstone(ctx, userID, fileHash)
	log.Printf("tombstone check: exists=%v status=%s err=%v", exists, status, err)
	
	if err != nil {
		log.Printf("Warning: check tombstone failed: %v", err)
	}

	if exists && status == "completed" {
		contentID, err := redis.GetTombstoneContentID(ctx, userID, fileHash)
		if err == nil && contentID > 0 {
			// 验证文件确实存在
			if Store.FileExists(fileHash) {
				return &InitUploadResult{
					ContentID: contentID,
					Status:    "fast_upload",
				}, nil
			}
			// 文件不存在，删除墓碑
			log.Printf("File not found on storage, deleting tombstone")
			_ = redis.DeleteTombstone(ctx, userID, fileHash)
		}
	}

	if exists && status == "cancelled" {
		_ = redis.DeleteTombstone(ctx, userID, fileHash)
	}

	// 2. 检查数据库是否已完成（双重保险）
	if !exists {
		if uc, err := db.GetUserContentByHash(ctx, userID, fileHash); err == nil && uc.Status == 1 {
			if fm, err := db.GetFileMeta(ctx, fileHash); err == nil && fm.FilePath != "" {
				if Store.FileExists(fileHash) {
					_ = redis.CreateTombstoneNoExpire(ctx, userID, fileHash, uc.ContentID, "completed")
					return &InitUploadResult{
						ContentID: uc.ContentID,
						Status:    "fast_upload",
					}, nil
				}
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

	// 5. 断点续传检查 - 只以文件系统为准！！！
	// Redis 可能因重启丢失数据，所以必须以文件系统为准
	uploadedChunks, err := Store.GetUploadedChunks(userID, fileHash)
	if err != nil {
		log.Printf("Warning: get uploaded chunks from filesystem failed: %v", err)
		uploadedChunks = []int{}
	}
	
	log.Printf("Filesystem chunks for user=%d hash=%s: %v", userID, fileHash, uploadedChunks)

	resultStatus := "new"
	if len(uploadedChunks) > 0 {
		resultStatus = "resumable"
		sort.Ints(uploadedChunks)
	}

	return &InitUploadResult{
		ContentID:      contentID,
		Status:         resultStatus,
		UploadedChunks: uploadedChunks,
	}, nil
}

// UploadChunkParams 上传分片参数
type UploadChunkParams struct {
	UserID      int
	FileHash    string
	ContentID   uint
	ChunkIndex  int
	TotalChunks int
	Content     io.Reader
}

// UploadChunk 上传分片
func UploadChunk(ctx context.Context, params UploadChunkParams) error {
	// 1. 检查墓碑
	if err := CheckBeforeUploadChunk(ctx, params.UserID, params.FileHash); err != nil {
		return err
	}

	// 2. 检查分片是否已存在于文件系统（幂等性）
	existingChunks, _ := Store.GetUploadedChunks(params.UserID, params.FileHash)
	for _, idx := range existingChunks {
		if idx == params.ChunkIndex {
			log.Printf("Chunk %d already exists on filesystem, skipping", params.ChunkIndex)
			return ErrChunkAlreadyUploaded
		}
	}

	// 3. 写入分片
	if err := Store.WriteChunk(params.UserID, params.FileHash, params.ChunkIndex, params.Content); err != nil {
		return fmt.Errorf("write chunk failed: %w", err)
	}

	log.Printf("Chunk %d written successfully for user=%d hash=%s", params.ChunkIndex, params.UserID, params.FileHash)

	// 4. 记录到 Redis（可选，仅用于加速，不作为唯一依据）
	if err := redis.RecordUploadedChunk(ctx, params.UserID, params.FileHash, params.ChunkIndex); err != nil {
		log.Printf("Warning: record chunk to redis failed: %v", err)
	}

	return nil
}

// MergeChunksParams 合并分片参数
type MergeChunksParams struct {
	UserID      int
	ContentID   uint
	FileName    string
	FileHash    string
	TotalChunks int
	FileSize    int64
}

// MergeChunksResult 合并结果
type MergeChunksResult struct {
	FilePath string
	FileSize int64
}

// MergeChunks 合并分片
func MergeChunks(ctx context.Context, params MergeChunksParams) (*MergeChunksResult, error) {
	// 1. 获取分布式锁
	lockKey := fmt.Sprintf("upload:merge:%d:%s", params.UserID, params.FileHash)
	lock := redis.NewLock(lockKey, 120*time.Second)
	if err := lock.Lock(ctx); err != nil {
		return nil, fmt.Errorf("acquire lock failed: %w", err)
	}
	defer lock.Unlock(ctx)

	// 2. 从文件系统验证所有分片
	uploadedChunks, err := Store.GetUploadedChunks(params.UserID, params.FileHash)
	if err != nil {
		return nil, fmt.Errorf("get uploaded chunks failed: %w", err)
	}

	log.Printf("MergeChunks: found %d chunks on filesystem, expected %d", len(uploadedChunks), params.TotalChunks)

	if len(uploadedChunks) != params.TotalChunks {
		missing := findMissingChunks(uploadedChunks, params.TotalChunks)
		return nil, fmt.Errorf("missing chunks: %v (have %d, need %d)", missing, len(uploadedChunks), params.TotalChunks)
	}

	// 3. 合并分片
	filePath, fileSize, err := Store.MergeChunks(params.UserID, params.FileHash, params.TotalChunks)
	if err != nil {
		return nil, fmt.Errorf("merge chunks failed: %w", err)
	}

	log.Printf("MergeChunks: merged to %s, size=%d", filePath, fileSize)

	// 4. 更新数据库
	if err := db.FinishMergeAndCreateMeta(ctx, params.UserID, params.ContentID, params.FileName, params.FileHash, filePath, fileSize); err != nil {
		Store.DeleteFile(params.FileHash)
		return nil, fmt.Errorf("update database failed: %w", err)
	}

	// 5. 清理 Redis 分片记录
	_ = redis.ClearUploadedChunks(ctx, params.UserID, params.FileHash)

	// 6. 创建墓碑
	if err := redis.CreateTombstone(ctx, params.UserID, params.FileHash, params.ContentID, "completed"); err != nil {
		log.Printf("create tombstone failed: %v", err)
	}

	return &MergeChunksResult{
		FilePath: filePath,
		FileSize: fileSize,
	}, nil
}

// findMissingChunks 找出缺失的分片
func findMissingChunks(uploaded []int, total int) []int {
	set := make(map[int]bool)
	for _, c := range uploaded {
		set[c] = true
	}

	var missing []int
	for i := 0; i < total; i++ {
		if !set[i] {
			missing = append(missing, i)
		}
	}
	return missing
}

// CheckBeforeUploadChunk 上传分块前检查墓碑
func CheckBeforeUploadChunk(ctx context.Context, userID int, fileHash string) error {
	exists, status, err := redis.CheckTombstone(ctx, userID, fileHash)
	if err != nil {
		log.Printf("Warning: check tombstone failed: %v", err)
		return nil // 不阻止上传
	}
	if exists && status == "completed" {
		return ErrUploadAlreadyCompleted
	}
	if exists && status == "cancelled" {
		return ErrUploadCancelled
	}
	return nil
}

// FastUpload 秒传
func FastUpload(ctx context.Context, userID int, contentID uint, fileName, fileHash string) error {
	lockKey := fmt.Sprintf("upload:fast:%d:%s", userID, fileHash)
	lock := redis.NewLock(lockKey, 30*time.Second)
	if err := lock.Lock(ctx); err != nil {
		return fmt.Errorf("acquire lock failed: %w", err)
	}
	defer lock.Unlock(ctx)

	if err := db.CreateUserFileForFastUpload(ctx, userID, contentID, fileName, fileHash); err != nil {
		return err
	}

	if err := redis.CreateTombstone(ctx, userID, fileHash, contentID, "completed"); err != nil {
		log.Printf("create tombstone failed: %v\n", err)
	}

	return nil
}

// CancelUpload 取消上传
func CancelUpload(ctx context.Context, userID int, fileHash string, contentID uint) error {
	lockKey := fmt.Sprintf("upload:cancel:%d:%s", userID, fileHash)
	lock := redis.NewLock(lockKey, 30*time.Second)
	if err := lock.Lock(ctx); err != nil {
		return fmt.Errorf("acquire lock failed: %w", err)
	}
	defer lock.Unlock(ctx)

	if err := db.UpdateUserContentStatus(ctx, userID, contentID, -1); err != nil {
		return err
	}

	// 清理分片文件
	_ = Store.CleanupChunks(userID, fileHash)

	return redis.CreateTombstone(ctx, userID, fileHash, contentID, "cancelled")
}

// DeleteFile 删除文件
func DeleteFile(ctx context.Context, userID int, fileHash string) error {
	lockKey := fmt.Sprintf("file:delete:%d:%s", userID, fileHash)
	lock := redis.NewLock(lockKey, 30*time.Second)
	if err := lock.Lock(ctx); err != nil {
		return fmt.Errorf("acquire lock failed: %w", err)
	}
	defer lock.Unlock(ctx)

	if err := db.DeleteUserFile(ctx, userID, fileHash); err != nil {
		return err
	}

	_ = redis.DeleteTombstone(ctx, userID, fileHash)

	return nil
}

// FileInfo 文件信息
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
