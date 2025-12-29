package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

const (
	ChunkSize = 5 * 1024 * 1024 // 5MB per chunk
	ServerURL = "http://localhost:8080"
)

// 定义服务器返回的响应结构
type InitResponse struct {
	Status         string   `json:"status"`          // "finished" or "uploading"
	FinishedChunks []string `json:"finished_chunks"` // 已存在的切片索引
	Url            string   `json:"url"`
	Msg            string   `json:"msg"`
}

func main() {
	filePath := "./test_video.mp4" // 准备一个测试视频
	
	// 1. 计算文件 MD5 (简单起见，一次性读取，大文件应流式计算)
	fmt.Println("正在计算 MD5...")
	fileContent, _ := os.ReadFile(filePath)
	hasher := md5.New()
	hasher.Write(fileContent)
	fileHash := hex.EncodeToString(hasher.Sum(nil))
	fmt.Printf("File MD5: %s\n", fileHash)
	initResp, err := checkFileStatus(fileHash)
	if err != nil {
		panic(err)
	}
	if initResp.Status == "finished" {
		fmt.Printf("✅ 秒传成功！文件已存在于: %s\n", initResp.Url)
		return // 直接结束，不执行后续逻辑
	}

	// 2. 调用 /init 检查状态
	// (此处省略 HTTP 请求代码，模拟返回：Server说还没上传)
	fmt.Println("开始分片上传...")

	// 3. 切片并并发上传
	file, _ := os.Open(filePath)
	defer file.Close()
	fi, _ := file.Stat()
	totalChunks := int(fi.Size()+ChunkSize-1) / ChunkSize

	var wg sync.WaitGroup
	// 限制并发数为 5
	semaphore := make(chan struct{}, 5)

	for i := 0; i < totalChunks; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			semaphore <- struct{}{} // 获取令牌
			defer func() { <-semaphore }() // 释放令牌

			// 读取指定分片数据
			partBuffer := make([]byte, ChunkSize)
			n,err  := file.ReadAt(partBuffer, int64(idx)*int64(ChunkSize))
			if n <= 0 {
                if err != nil && err != io.EOF {
                    fmt.Printf("read chunk %d failed: %v\n", idx, err)
                }
                return
            }

			uploadChunk(fileHash, idx, partBuffer[:n])
		}(i)
	}
	wg.Wait()

	// 4. 发送合并请求
	fmt.Println("所有分片上传完毕，请求合并...")
	sendMergeRequest(fileHash, totalChunks, filepath.Base(filePath))
}

func checkFileStatus(hash string) (*InitResponse, error) {
	resp, err := http.PostForm(ServerURL+"/upload/init", url.Values{"file_hash": {hash}})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result InitResponse
	// 解析 JSON
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func uploadChunk(hash string, index int, data []byte) {
	// 构建 Multipart 表单上传
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("file_hash", hash)
	writer.WriteField("index", strconv.Itoa(index))
	part, err := writer.CreateFormFile("data", fmt.Sprintf("chunk_%d", index))
    if err != nil {
        fmt.Printf("create form file failed: %v\n", err)
        return
    }
    _, _ = io.Copy(part, bytes.NewReader(data))
    writer.Close()

    resp, err := http.Post(ServerURL+"/upload/chunk", writer.FormDataContentType(), body)
    if err != nil {
        fmt.Printf("upload chunk %d failed: %v\n", index, err)
        return
    }
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
    fmt.Printf("Chunk %d uploaded, status=%s, resp=%s\n", index, resp.Status, string(respBody))
}

func sendMergeRequest(hash string, total int, filename string) {
    v := url.Values{}
    v.Set("file_hash", hash)
    v.Set("total_chunks", strconv.Itoa(total))
    v.Set("file_name", filename)

    resp, err := http.PostForm(ServerURL+"/upload/merge", v)
    if err != nil {
        fmt.Printf("merge request failed: %v\n", err)
        return
    }
    defer resp.Body.Close()
    body, _ := io.ReadAll(resp.Body)
    if resp.StatusCode != http.StatusOK {
        fmt.Printf("merge failed, status=%s, body=%s\n", resp.Status, string(body))
        return
    }
    fmt.Printf("merge success: %s\n", string(body))
    fmt.Println("🎉 上传并合并成功！")
}