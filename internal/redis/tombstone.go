package redis

import (
	"context"
	"fmt"
	"time"
)

const (
	TombstonePrefix = "tombstone:"
	TombstoneTTL    = 24 * time.Hour // 墓碑保留 24 小时
)

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

// CheckTombstone 检查墓碑是否存在
// 返回：(是否存在, 状态, 错误)
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

// DeleteTombstone 删除墓碑（用于重新上传）
func DeleteTombstone(ctx context.Context, userID int, fileHash string) error {
	key := fmt.Sprintf("%s%d:%s", TombstonePrefix, userID, fileHash)
	return Client.Del(ctx, key).Err()
}

// GetTombstoneContentID 获取墓碑中的 ContentID（秒传使用）
func GetTombstoneContentID(ctx context.Context, userID int, fileHash string) (uint, error) {
	key := fmt.Sprintf("%s%d:%s", TombstonePrefix, userID, fileHash)
	contentID, err := Client.HGet(ctx, key, "content_id").Uint64()
	if err != nil {
		return 0, err
	}
	return uint(contentID), nil
}
