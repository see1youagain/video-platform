package store

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Uploader 定义了文件如何保存
type Uploader interface {
    // WriteChunk 写入分片
    WriteChunk(hash string, index int, content io.Reader) error
    // MergeChunks 合并分片
    MergeChunks(hash string, totalChunks int) (string, error)
}

// LocalStore 本地文件系统实现
type LocalStore struct {
    BasePath string
    TempPath string
}


func (s *LocalStore) ensureDirs() error {
    if s.TempPath == "" || s.BasePath == "" {
        return fmt.Errorf("BasePath or TempPath not set")
    }
    if err := os.MkdirAll(s.TempPath, 0755); err != nil {
        return err
    }
    if err := os.MkdirAll(s.BasePath, 0755); err != nil {
        return err
    }
    return nil
}

func chunkName(hash string, index int) string {
    return fmt.Sprintf("%s.part.%d", hash, index)
}

// WriteChunk 将单个分片写入临时目录，index 可从 0 或 1 开始（合并时会兼容）
func (s *LocalStore) WriteChunk(hash string, index int, content io.Reader) error {
    if err := s.ensureDirs(); err != nil {
        return err
    }
    fname := filepath.Join(s.TempPath, chunkName(hash, index))
    f, err := os.Create(fname)
    if err != nil {
        return fmt.Errorf("create chunk file: %w", err)
    }
    defer f.Close()
    if _, err := io.Copy(f, content); err != nil {
        return fmt.Errorf("write chunk content: %w", err)
    }
    return nil
}

// findPart 尝试按 index 或 index+1 找到分片文件（兼容从 0/1 开始）
func (s *LocalStore) findPart(hash string, idx int) (string, error) {
    candidates := []string{
        filepath.Join(s.TempPath, chunkName(hash, idx)),
        filepath.Join(s.TempPath, chunkName(hash, idx+1)),
    }
    for _, p := range candidates {
        if _, err := os.Stat(p); err == nil {
            return p, nil
        }
    }
    return "", fmt.Errorf("chunk not found for index %d", idx)
}

// MergeChunks 将所有分片按索引顺序合并到目标文件并删除分片，返回合并后文件路径
func (s *LocalStore) MergeChunks(hash string, totalChunks int) (string, error) {
    if err := s.ensureDirs(); err != nil {
        return "", err
    }
    destPath := filepath.Join(s.BasePath, hash)
    tmpDest := destPath + ".tmp"

    out, err := os.Create(tmpDest)
    if err != nil {
        return "", fmt.Errorf("create dest file: %w", err)
    }

    for i := 0; i < totalChunks; i++ {
        partFile, err := s.findPart(hash, i)
        if err != nil {
            out.Close()
            os.Remove(tmpDest)
            return "", fmt.Errorf("find part %d: %w", i, err)
        }
        pf, err := os.Open(partFile)
        if err != nil {
            out.Close()
            os.Remove(tmpDest)
            return "", fmt.Errorf("open part %s: %w", partFile, err)
        }
        if _, err := io.Copy(out, pf); err != nil {
            pf.Close()
            out.Close()
            os.Remove(tmpDest)
            return "", fmt.Errorf("copy part %s: %w", partFile, err)
        }
        pf.Close()
        // 删除分片文件
        _ = os.Remove(partFile)
    }

    if err := out.Close(); err != nil {
        os.Remove(tmpDest)
        return "", fmt.Errorf("close dest file: %w", err)
    }

    if err := os.Rename(tmpDest, destPath); err != nil {
        return "", fmt.Errorf("rename temp to dest: %w", err)
    }

    return destPath, nil
}

// 重点：未来只需新增一个 MinIOStore 实现这个接口即可。