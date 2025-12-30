package redis

import (
	"context"
	"fmt"
	"log"
	"time"
)

const (
	TombstonePrefix = "tombstone:"
	TombstoneTTL    = 24 * time.Hour // 墓碑保留 24 小时
)

// TombstoneData 用于从数据库加载的墓碑数据
type TombstoneData struct {
	UserID    int
	FileHash  string
	ContentID uint
	Status    string
}

// CreateTombstone 创建墓碑（上传完成后调用）
func CreateTombstone(ctx context.Context, userID int, fileHash string, contentID uint, status string) error {
	key := fmt.Sprintf("%s%d:%s", TombstonePrefix, userID, fileHash)
	tombstone := map[string]interface{}{
		"user_id":    userID,
		"file_hash":  fileHash,
		"content_id": contentID,
		"status":     status,
		"created_at": time.Now().Unix(),
	}
	if err := Client.HSet(ctx, key, tombstone).Err(); err != nil {
		return err
	}
	return Client.Expire(ctx, key, TombstoneTTL).Err()
}

// CreateTombstoneNoExpire 创建不过期的墓碑（用于启动时加载）
func CreateTombstoneNoExpire(ctx context.Context, userID int, fileHash string, contentID uint, status string) error {
	key := fmt.Sprintf("%s%d:%s", TombstonePrefix, userID, fileHash)
	tombstone := map[string]interface{}{
		"user_id":    userID,
		"file_hash":  fileHash,
		"content_id": contentID,
		"status":     status,
		"created_at": time.Now().Unix(),
	}
	return Client.HSet(ctx, key, tombstone).Err()
}

// CheckTombstone 检查墓碑是否存在
func CheckTombstone(ctx context.Context, userID int, fileHash string) (bool, string, error) {
	key := fmt.Sprintf("%s%d:%s", TombstonePrefix, userID, fileHash)
	exists, err := Client.Exists(ctx, key).Result()
	if err != nil {
		return false, "", err
	}
	if exists == 0 {
		return false, "", nil
	}

	status, err := Client.HGet(ctx, key, "status").Result()
	if err != nil {
		return true, "", err
	}
	return true, status, nil
}

// DeleteTombstone 删除墓碑
func DeleteTombstone(ctx context.Context, userID int, fileHash string) error {
	key := fmt.Sprintf("%s%d:%s", TombstonePrefix, userID, fileHash)
	return Client.Del(ctx, key).Err()
}

// GetTombstoneContentID 获取墓碑中的 ContentID
func GetTombstoneContentID(ctx context.Context, userID int, fileHash string) (uint, error) {
	key := fmt.Sprintf("%s%d:%s", TombstonePrefix, userID, fileHash)
	contentID, err := Client.HGet(ctx, key, "content_id").Uint64()
	if err != nil {
		return 0, err
	}
	return uint(contentID), nil
}

// LoadTombstonesFromDB 从数据库加载墓碑到 Redis（服务器启动时调用）
func LoadTombstonesFromDB(ctx context.Context, tombstones []TombstoneData) error {
	if len(tombstones) == 0 {
		log.Println("No tombstones to load")
		return nil
	}

	log.Printf("Loading %d tombstones from database...", len(tombstones))
	
	// 使用 Pipeline 批量写入提高性能
	pipe := Client.Pipeline()
	
	for _, t := range tombstones {
		key := fmt.Sprintf("%s%d:%s", TombstonePrefix, t.UserID, t.FileHash)
		tombstone := map[string]interface{}{
			"user_id":    t.UserID,
			"file_hash":  t.FileHash,
			"content_id": t.ContentID,
			"status":     t.Status,
			"created_at": time.Now().Unix(),
		}
		pipe.HSet(ctx, key, tombstone)
		// 已完成的上传不设置过期时间，保证秒传一直有效
		// 取消的上传设置过期时间
		if t.Status == "cancelled" {
			pipe.Expire(ctx, key, TombstoneTTL)
		}
	}
	
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to load tombstones: %w", err)
	}
	
	log.Printf("Successfully loaded %d tombstones", len(tombstones))
	return nil
}

// CountTombstones 统计当前 Redis 中的墓碑数量
func CountTombstones(ctx context.Context) (int64, error) {
	keys, err := Client.Keys(ctx, TombstonePrefix+"*").Result()
	if err != nil {
		return 0, err
	}
	return int64(len(keys)), nil
}
