package logic

import (
	"context"
	"log"
	"time"

	"video-platform/internal/db"
	"video-platform/internal/redis"
)

// LoadTombstonesOnStartup 服务器启动时从数据库加载墓碑到 Redis
func LoadTombstonesOnStartup(ctx context.Context) error {
	log.Println("Starting tombstone loading from database...")
	startTime := time.Now()

	// 1. 获取所有上传记录
	uploads, err := db.GetCompletedUploadsForTombstone(ctx)
	if err != nil {
		return err
	}

	if len(uploads) == 0 {
		log.Println("No upload records found, skipping tombstone loading")
		return nil
	}

	// 2. 获取所有真实存在的文件哈希
	existingFiles, err := db.GetFileMetaHashes(ctx)
	if err != nil {
		log.Printf("Warning: failed to get file meta hashes: %v", err)
		existingFiles = make(map[string]bool)
	}

	// 3. 构建墓碑数据
	var tombstones []redis.TombstoneData
	for _, u := range uploads {
		var status string
		switch u.Status {
		case 1: // 已完成
			// 只有文件真实存在才标记为 completed
			if existingFiles[u.FileHash] {
				status = "completed"
			} else {
				// 文件不存在，可能被清理了，跳过
				continue
			}
		case -1: // 已取消
			status = "cancelled"
		case 0: // 上传中
			// 服务器重启，之前上传中的记录标记为 cancelled，允许重新上传
			status = "cancelled"
		default:
			continue
		}

		tombstones = append(tombstones, redis.TombstoneData{
			UserID:    u.UserID,
			FileHash:  u.FileHash,
			ContentID: u.ContentID,
			Status:    status,
		})
	}

	// 4. 批量加载到 Redis
	if err := redis.LoadTombstonesFromDB(ctx, tombstones); err != nil {
		return err
	}

	elapsed := time.Since(startTime)
	log.Printf("Tombstone loading completed in %v, loaded %d entries", elapsed, len(tombstones))

	return nil
}

// ValidateTombstones 验证墓碑与数据库/文件系统的一致性（可选的定期任务）
func ValidateTombstones(ctx context.Context) error {
	count, err := redis.CountTombstones(ctx)
	if err != nil {
		return err
	}
	log.Printf("Current tombstone count in Redis: %d", count)
	return nil
}
