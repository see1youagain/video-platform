package redis

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

const (
	ChunkPrefix = "chunks:"
	ChunkTTL    = 24 * time.Hour // 分片记录保留 24 小时
)

// RecordUploadedChunk 记录已上传的分片
func RecordUploadedChunk(ctx context.Context, userID int, fileHash string, chunkIndex int) error {
	key := fmt.Sprintf("%s%d:%s", ChunkPrefix, userID, fileHash)
	if err := Client.SAdd(ctx, key, chunkIndex).Err(); err != nil {
		return err
	}
	return Client.Expire(ctx, key, ChunkTTL).Err()
}

// GetUploadedChunks 获取已上传的分片列表
func GetUploadedChunks(ctx context.Context, userID int, fileHash string) ([]int, error) {
	key := fmt.Sprintf("%s%d:%s", ChunkPrefix, userID, fileHash)
	members, err := Client.SMembers(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	chunks := make([]int, 0, len(members))
	for _, m := range members {
		if idx, err := strconv.Atoi(m); err == nil {
			chunks = append(chunks, idx)
		}
	}
	return chunks, nil
}

// ClearUploadedChunks 清除分片记录（合并完成后调用）
func ClearUploadedChunks(ctx context.Context, userID int, fileHash string) error {
	key := fmt.Sprintf("%s%d:%s", ChunkPrefix, userID, fileHash)
	return Client.Del(ctx, key).Err()
}

// GetUploadedChunkCount 获取已上传分片数量
func GetUploadedChunkCount(ctx context.Context, userID int, fileHash string) (int64, error) {
	key := fmt.Sprintf("%s%d:%s", ChunkPrefix, userID, fileHash)
	return Client.SCard(ctx, key).Result()
}

// IsChunkUploaded 检查分片是否已上传
func IsChunkUploaded(ctx context.Context, userID int, fileHash string, chunkIndex int) (bool, error) {
	key := fmt.Sprintf("%s%d:%s", ChunkPrefix, userID, fileHash)
	return Client.SIsMember(ctx, key, chunkIndex).Result()
}
