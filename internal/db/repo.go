package db

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrUploadAlreadyCompleted = errors.New("upload already completed")
	ErrUploadCancelled        = errors.New("upload was cancelled")
)

// CreateOrUpdateUserFileUploading：upload/init 时调用（纯数据库操作）
func CreateOrUpdateUserFileUploading(ctx context.Context, userID int, fileName, fileHash string) (uint, error) {
	tx := DB.WithContext(ctx).Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	now := time.Now()
	var ct Content
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("source_hash = ? AND owner_id = ?", fileHash, userID).
		First(&ct).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ct = Content{
				OwnerID:    userID,
				SourceHash: fileHash,
				Title:      fileName,
				CreatedAt:  now,
			}
			if err := tx.Create(&ct).Error; err != nil {
				tx.Rollback()
				return 0, err
			}
		} else {
			tx.Rollback()
			return 0, err
		}
	}

	uc := UserContent{}
	err := tx.Where("user_id = ? AND content_id = ?", userID, ct.ID).First(&uc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		uc = UserContent{
			UserID:    userID,
			ContentID: ct.ID,
			FileName:  fileName,
			FileHash:  fileHash,
			Status:     0,
			CreatedAt: now,
		}
		if err := tx.Create(&uc).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
	} else if err != nil {
		tx.Rollback()
		return 0, err
	} else {
		if err := tx.Model(&uc).Updates(map[string]interface{}{
			"status":     0,
			"updated_at": now,
		}).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return ct.ID, nil
}

// CreateUserFileForFastUpload：秒传命中时调用（纯数据库操作）
func CreateUserFileForFastUpload(ctx context.Context, userID int, contentID uint, fileName, fileHash string) error {
	tx := DB.WithContext(ctx).Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	var ct Content
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ? AND owner_id = ?", contentID, userID).
		First(&ct).Error; err != nil {
		tx.Rollback()
		return err
	}

	if err := tx.Model(&FileMeta{}).
		Where("file_hash = ?", fileHash).
		UpdateColumn("ref_count", gorm.Expr("ref_count + ?", 1)).Error; err != nil {
		tx.Rollback()
		return err
	}

	uc := UserContent{}
	if err := tx.Where("user_id = ? AND content_id = ?", userID, contentID).First(&uc).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			uc = UserContent{
				UserID:    userID,
				ContentID: contentID,
				FileName:  fileName,
				FileHash:  fileHash,
				Status:    1,
				CreatedAt: time.Now(),
			}
			if err := tx.Create(&uc).Error; err != nil {
				tx.Rollback()
				return err
			}
		} else {
			tx.Rollback()
			return err
		}
	} else {
		if err := tx.Model(&uc).Updates(map[string]interface{}{
			"status":    1,
			"file_name": fileName,
		}).Error; err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit().Error
}

// FinishMergeAndCreateMeta：合并分块成功后调用（纯数据库操作）
func FinishMergeAndCreateMeta(ctx context.Context, userID int, contentID uint, fileName, fileHash, filePath string, fileSize int64) error {
	tx := DB.WithContext(ctx).Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	var fm FileMeta
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("file_hash = ?", fileHash).First(&fm).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			fm = FileMeta{
				FileHash:  fileHash,
				ContentID: contentID,
				FilePath:  filePath,
				FileSize:  fileSize,
				RefCount:  1,
				CreatedAt: time.Now(),
			}
			if err := tx.Create(&fm).Error; err != nil {
				tx.Rollback()
				return err
			}
		} else {
			tx.Rollback()
			return err
		}
	} else {
		if err := tx.Model(&fm).UpdateColumn("ref_count", gorm.Expr("ref_count + ?", 1)).Error; err != nil {
			tx.Rollback()
			return err
		}
	}

	if err := tx.Model(&UserContent{}).
		Where("user_id = ? AND content_id = ?", userID, contentID).
		Updates(map[string]interface{}{
			"status":     1,
			"file_hash":  fileHash,
			"updated_at": time.Now(),
		}).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// DeleteUserFile：删除用户文件（纯数据库操作）
func DeleteUserFile(ctx context.Context, userID int, fileHash string) error {
	tx := DB.WithContext(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := tx.Where("user_id = ? AND file_hash = ?", userID, fileHash).
		Delete(&UserContent{}).Error; err != nil {
		return err
	}

	var fm FileMeta
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("file_hash = ?", fileHash).First(&fm).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return tx.Commit().Error
		}
		return err
	}

	if err := tx.Model(&FileMeta{}).
		Where("file_hash = ?", fileHash).
		UpdateColumn("ref_count", gorm.Expr("GREATEST(ref_count - ?, 0)", 1)).Error; err != nil {
		return err
	}

	if err := tx.Where("file_hash = ?", fileHash).First(&fm).Error; err != nil {
		return err
	}

	if fm.RefCount <= 0 {
		if fm.FilePath != "" {
			_ = os.Remove(filepath.Clean(fm.FilePath))
		}
		if err := tx.Where("file_hash = ?", fileHash).Delete(&FileMeta{}).Error; err != nil {
			return err
		}
	}

	return tx.Commit().Error
}

// UpdateUserContentStatus 更新用户内容状态
func UpdateUserContentStatus(ctx context.Context, userID int, contentID uint, status int) error {
	return DB.WithContext(ctx).Model(&UserContent{}).
		Where("user_id = ? AND content_id = ? AND status = 0", userID, contentID).
		Update("status", status).Error
}

// GetUserContents 获取用户的所有文件记录
func GetUserContents(ctx context.Context, userID int) ([]UserContent, error) {
	var userContents []UserContent
	err := DB.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&userContents).Error
	return userContents, err
}

// GetUserContentByHash 根据 hash 获取用户的文件记录
func GetUserContentByHash(ctx context.Context, userID int, fileHash string) (*UserContent, error) {
	var uc UserContent
	err := DB.WithContext(ctx).
		Where("user_id = ? AND file_hash = ?", userID, fileHash).
		First(&uc).Error
	if err != nil {
		return nil, err
	}
	return &uc, nil
}

// GetFileMeta 获取文件元数据
func GetFileMeta(ctx context.Context, fileHash string) (*FileMeta, error) {
	var fm FileMeta
	err := DB.WithContext(ctx).
		Where("file_hash = ?", fileHash).
		First(&fm).Error
	if err != nil {
		return nil, err
	}
	return &fm, nil
}

// GetContentsByOwner 获取用户的所有内容
func GetContentsByOwner(ctx context.Context, userID int) ([]Content, error) {
	var contents []Content
	err := DB.WithContext(ctx).
		Where("owner_id = ?", userID).
		Order("created_at DESC").
		Find(&contents).Error
	return contents, err
}

// GetContentByID 获取单个内容
func GetContentByID(ctx context.Context, userID int, contentID string) (*Content, error) {
	var content Content
	err := DB.WithContext(ctx).
		Where("id = ? AND owner_id = ?", contentID, userID).
		First(&content).Error
	if err != nil {
		return nil, err
	}
	return &content, nil
}

// GetCompletedUploadsForTombstone 获取所有已完成的上传记录（用于启动时加载墓碑）
func GetCompletedUploadsForTombstone(ctx context.Context) ([]struct {
	UserID    int
	FileHash  string
	ContentID uint
	Status    int
}, error) {
	var results []struct {
		UserID    int
		FileHash  string
		ContentID uint
		Status    int
	}

	// 查询所有有 FileHash 的 UserContent 记录
	err := DB.WithContext(ctx).
		Model(&UserContent{}).
		Select("user_id, file_hash, content_id, status").
		Where("file_hash != '' AND file_hash IS NOT NULL").
		Find(&results).Error

	return results, err
}

// GetFileMetaHashes 获取所有存在的文件哈希（用于验证文件是否真实存在）
func GetFileMetaHashes(ctx context.Context) (map[string]bool, error) {
	var hashes []string
	err := DB.WithContext(ctx).
		Model(&FileMeta{}).
		Pluck("file_hash", &hashes).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string]bool)
	for _, h := range hashes {
		result[h] = true
	}
	return result, nil
}