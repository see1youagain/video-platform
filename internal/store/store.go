package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Uploader 定义文件存储接口
type Uploader interface {
	WriteChunk(userID int, hash string, index int, content io.Reader) error
	MergeChunks(userID int, hash string, totalChunks int) (filePath string, fileSize int64, err error)
	GetUploadedChunks(userID int, hash string) ([]int, error)
	CleanupChunks(userID int, hash string) error
	GetFile(hash string) (io.ReadCloser, int64, error)
	GetFileRange(hash string, start, end int64) (io.ReadCloser, error)
	DeleteFile(hash string) error
	FileExists(hash string) bool
}

// LocalStore 本地文件系统实现
type LocalStore struct {
	BasePath string
	TempPath string
}

// NewLocalStore 创建本地存储
func NewLocalStore(basePath, tempPath string) *LocalStore {
	// 确保目录存在
	os.MkdirAll(basePath, 0755)
	os.MkdirAll(tempPath, 0755)
	return &LocalStore{
		BasePath: basePath,
		TempPath: tempPath,
	}
}

func (s *LocalStore) getChunkDir(userID int, hash string) string {
	return filepath.Join(s.TempPath, fmt.Sprintf("%d", userID), hash)
}

func (s *LocalStore) getChunkPath(userID int, hash string, index int) string {
	return filepath.Join(s.getChunkDir(userID, hash), fmt.Sprintf("%d.part", index))
}

func (s *LocalStore) getFilePath(hash string) string {
	return filepath.Join(s.BasePath, hash)
}

// WriteChunk 写入分片
func (s *LocalStore) WriteChunk(userID int, hash string, index int, content io.Reader) error {
	chunkDir := s.getChunkDir(userID, hash)
	if err := os.MkdirAll(chunkDir, 0755); err != nil {
		return fmt.Errorf("create chunk dir failed: %w", err)
	}

	chunkPath := s.getChunkPath(userID, hash, index)
	tmpPath := chunkPath + ".tmp"

	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create chunk file failed: %w", err)
	}

	written, err := io.Copy(f, content)
	if err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("write chunk failed: %w", err)
	}
	f.Close()

	if written == 0 {
		os.Remove(tmpPath)
		return fmt.Errorf("empty chunk data")
	}

	if err := os.Rename(tmpPath, chunkPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename chunk failed: %w", err)
	}

	return nil
}

// GetUploadedChunks 获取已上传的分片索引
func (s *LocalStore) GetUploadedChunks(userID int, hash string) ([]int, error) {
	chunkDir := s.getChunkDir(userID, hash)

	entries, err := os.ReadDir(chunkDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []int{}, nil
		}
		return nil, err
	}

	var chunks []int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".part") {
			indexStr := strings.TrimSuffix(name, ".part")
			if index, err := strconv.Atoi(indexStr); err == nil {
				chunks = append(chunks, index)
			}
		}
	}

	sort.Ints(chunks)
	return chunks, nil
}

// MergeChunks 合并分片
func (s *LocalStore) MergeChunks(userID int, hash string, totalChunks int) (string, int64, error) {
	if err := os.MkdirAll(s.BasePath, 0755); err != nil {
		return "", 0, fmt.Errorf("create base dir failed: %w", err)
	}

	destPath := s.getFilePath(hash)
	tmpDest := destPath + ".tmp"

	out, err := os.Create(tmpDest)
	if err != nil {
		return "", 0, fmt.Errorf("create dest file failed: %w", err)
	}

	var totalSize int64

	for i := 0; i < totalChunks; i++ {
		chunkPath := s.getChunkPath(userID, hash, i)

		pf, err := os.Open(chunkPath)
		if err != nil {
			out.Close()
			os.Remove(tmpDest)
			return "", 0, fmt.Errorf("open chunk %d failed: %w", i, err)
		}

		written, err := io.Copy(out, pf)
		pf.Close()

		if err != nil {
			out.Close()
			os.Remove(tmpDest)
			return "", 0, fmt.Errorf("copy chunk %d failed: %w", i, err)
		}

		totalSize += written
	}

	if err := out.Close(); err != nil {
		os.Remove(tmpDest)
		return "", 0, fmt.Errorf("close dest file failed: %w", err)
	}

	if err := os.Rename(tmpDest, destPath); err != nil {
		os.Remove(tmpDest)
		return "", 0, fmt.Errorf("rename to dest failed: %w", err)
	}

	// 清理分片
	s.CleanupChunks(userID, hash)

	return destPath, totalSize, nil
}

// CleanupChunks 清理分片临时文件
func (s *LocalStore) CleanupChunks(userID int, hash string) error {
	chunkDir := s.getChunkDir(userID, hash)
	return os.RemoveAll(chunkDir)
}

// GetFile 获取文件
func (s *LocalStore) GetFile(hash string) (io.ReadCloser, int64, error) {
	filePath := s.getFilePath(hash)

	fi, err := os.Stat(filePath)
	if err != nil {
		return nil, 0, err
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, 0, err
	}

	return f, fi.Size(), nil
}

// GetFileRange 获取文件指定范围
func (s *LocalStore) GetFileRange(hash string, start, end int64) (io.ReadCloser, error) {
	filePath := s.getFilePath(hash)

	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}

	if _, err := f.Seek(start, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}

	length := end - start + 1
	return &limitedReadCloser{
		Reader: io.LimitReader(f, length),
		Closer: f,
	}, nil
}

// DeleteFile 删除文件
func (s *LocalStore) DeleteFile(hash string) error {
	filePath := s.getFilePath(hash)
	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// FileExists 检查文件是否存在
func (s *LocalStore) FileExists(hash string) bool {
	filePath := s.getFilePath(hash)
	_, err := os.Stat(filePath)
	return err == nil
}

type limitedReadCloser struct {
	io.Reader
	io.Closer
}
