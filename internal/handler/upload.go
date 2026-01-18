package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"video-platform/internal/logic"

	"github.com/gin-gonic/gin"
)

// InitUploadRequest 初始化上传请求
type InitUploadRequest struct {
	FileName string `json:"file_name" binding:"required"`
	FileHash string `json:"file_hash" binding:"required"`
	FileSize int64  `json:"file_size" binding:"required"`
}

// InitUpload 初始化上传
func InitUpload(c *gin.Context) {
	var req InitUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := getUserID(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	result, err := logic.InitUpload(ctx, userID, req.FileName, req.FileHash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":          result.Status,
		"content_id":      result.ContentID,
		"uploaded_chunks": result.UploadedChunks,
	})
}

// UploadChunkRequest 上传分块请求
type UploadChunkRequest struct {
	ContentID   uint   `form:"content_id" binding:"required"`
	FileHash    string `form:"file_hash" binding:"required"`
	ChunkIndex  int    `form:"chunk_index"`
	TotalChunks int    `form:"total_chunks" binding:"required"`
}

// UploadChunk 上传分块
func UploadChunk(c *gin.Context) {
	var req UploadChunkRequest
	if err := c.ShouldBind(&req); err != nil {
		log.Printf("UploadChunk bind error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := getUserID(c)
	if userID == 0 {
		log.Printf("UploadChunk: userID is 0")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	// 获取分块文件
	file, err := c.FormFile("chunk")
	if err != nil {
		log.Printf("UploadChunk FormFile error: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "No chunk file provided: " + err.Error()})
		return
	}

	log.Printf("UploadChunk: userID=%d, hash=%s, index=%d, fileSize=%d",
		userID, req.FileHash, req.ChunkIndex, file.Size)

	// 打开文件
	src, err := file.Open()
	if err != nil {
		log.Printf("UploadChunk Open error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open chunk file: " + err.Error()})
		return
	}
	defer src.Close()

	// 上传分片
	params := logic.UploadChunkParams{
		UserID:      userID,
		FileHash:    req.FileHash,
		ContentID:   req.ContentID,
		ChunkIndex:  req.ChunkIndex,
		TotalChunks: req.TotalChunks,
		Content:     src,
	}

	err = logic.UploadChunk(ctx, params)
	if err != nil {
		log.Printf("UploadChunk logic error: %v", err)
		if errors.Is(err, logic.ErrUploadAlreadyCompleted) {
			c.JSON(http.StatusConflict, gin.H{
				"error":   "Upload already completed",
				"message": "This file has been uploaded",
			})
			return
		}
		if errors.Is(err, logic.ErrUploadCancelled) {
			c.JSON(http.StatusGone, gin.H{
				"error":   "Upload cancelled",
				"message": "Please reinitialize upload",
			})
			return
		}
		if errors.Is(err, logic.ErrChunkAlreadyUploaded) {
			// 幂等性：分片已上传，返回成功
			c.JSON(http.StatusOK, gin.H{
				"status":      "chunk_exists",
				"chunk_index": req.ChunkIndex,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.Printf("UploadChunk success: index=%d", req.ChunkIndex)
	c.JSON(http.StatusOK, gin.H{
		"status":      "chunk_uploaded",
		"chunk_index": req.ChunkIndex,
	})
}

// MergeChunksRequest 合并分块请求
type MergeChunksRequest struct {
	ContentID   uint   `json:"content_id" binding:"required"`
	FileHash    string `json:"file_hash" binding:"required"`
	FileName    string `json:"file_name" binding:"required"`
	TotalChunks int    `json:"total_chunks" binding:"required"`
	FileSize    int64  `json:"file_size" binding:"required"`
}

// MergeChunks 合并分块
func MergeChunks(c *gin.Context) {
	var req MergeChunksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := getUserID(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 300*time.Second) // 大文件合并可能需要更长时间
	defer cancel()

	params := logic.MergeChunksParams{
		UserID:      userID,
		ContentID:   req.ContentID,
		FileName:    req.FileName,
		FileHash:    req.FileHash,
		TotalChunks: req.TotalChunks,
		FileSize:    req.FileSize,
	}

	result, err := logic.MergeChunks(ctx, params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "completed",
		"content_id": req.ContentID,
		"file_path":  result.FilePath,
		"file_size":  result.FileSize,
	})
}

// FastUploadRequest 秒传请求
type FastUploadRequest struct {
	ContentID uint   `json:"content_id" binding:"required"`
	FileName  string `json:"file_name" binding:"required"`
	FileHash  string `json:"file_hash" binding:"required"`
}

// FastUpload 秒传
func FastUpload(c *gin.Context) {
	var req FastUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := getUserID(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	if err := logic.FastUpload(ctx, userID, req.ContentID, req.FileName, req.FileHash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "fast_upload_completed",
		"content_id": req.ContentID,
	})
}

// CancelUploadRequest 取消上传请求
type CancelUploadRequest struct {
	ContentID uint   `json:"content_id" binding:"required"`
	FileHash  string `json:"file_hash" binding:"required"`
}

// CancelUpload 取消上传
func CancelUpload(c *gin.Context) {
	var req CancelUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := getUserID(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	if err := logic.CancelUpload(ctx, userID, req.FileHash, req.ContentID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "cancelled",
		"message": "Upload cancelled successfully",
	})
}

// getUserID 从请求上下文中获取用户 ID
func getUserID(c *gin.Context) int {
	userID, exists := c.Get("user_id")
	if !exists {
		return 0
	}
	if id, ok := userID.(int); ok {
		return id
	}
	return 0
}

// ListFiles 列出用户的文件
func ListFiles(c *gin.Context) {
	userID := getUserID(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}

	files, err := logic.ListUserFiles(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"files": files})
}

// GetFile 获取文件详情
func GetFile(c *gin.Context) {
	userID := getUserID(c)
	fileHash := c.Param("id")

	file, err := logic.GetUserFile(c.Request.Context(), userID, fileHash)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
		return
	}

	c.JSON(http.StatusOK, file)
}

// DeleteFile 删除文件
func DeleteFile(c *gin.Context) {
	userID := getUserID(c)
	fileHash := c.Param("id")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()

	if err := logic.DeleteFile(ctx, userID, fileHash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ListContents 列出用户的内容
func ListContents(c *gin.Context) {
	userID := getUserID(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "未登录"})
		return
	}

	contents, err := logic.ListUserContents(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"contents": contents})
}

// GetContent 获取内容详情
func GetContent(c *gin.Context) {
	userID := getUserID(c)
	contentID := c.Param("id")

	content, err := logic.GetUserContent(c.Request.Context(), userID, contentID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "内容不存在"})
		return
	}

	c.JSON(http.StatusOK, content)
}

// DownloadFile 下载文件
func DownloadFile(c *gin.Context) {
	userID := getUserID(c)
	fileHash := c.Param("id")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	rangeHeader := c.GetHeader("Range")

	result, err := logic.DownloadFile(ctx, userID, fileHash, rangeHeader)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	defer result.Reader.Close()

	// 设置响应头
	c.Header("Content-Type", result.ContentType)
	c.Header("Accept-Ranges", "bytes")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, result.FileName))

	if result.IsRange {
		// Range 响应
		c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d",
			result.RangeStart, result.RangeEnd, result.FileSize))
		c.Header("Content-Length", strconv.FormatInt(result.RangeLength, 10))
		c.Status(http.StatusPartialContent)
	} else {
		c.Header("Content-Length", strconv.FormatInt(result.FileSize, 10))
		c.Status(http.StatusOK)
	}

	// 流式传输
	c.Stream(func(w io.Writer) bool {
		_, err := io.Copy(w, result.Reader)
		return err == nil
	})
}
