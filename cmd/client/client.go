package main

import (
	"bufio"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ChunkSize = 5 * 1024 * 1024
	ServerURL = "http://localhost:8080/api/v1"
)

var (
	authToken string
	username  string
)

type AuthResponse struct {
	Token string `json:"token"`
	Msg   string `json:"msg"`
	Error string `json:"error"`
}

type InitResponse struct {
	Status         string `json:"status"`
	ContentID      uint   `json:"content_id"`
	UploadedChunks []int  `json:"uploaded_chunks"`
	Error          string `json:"error"`
}

type FileInfo struct {
	ID        uint   `json:"id"`
	FileName  string `json:"file_name"`
	FileHash  string `json:"file_hash"`
	FileSize  int64  `json:"file_size"`
	Status    int    `json:"status"`
	CreatedAt string `json:"created_at"`
}

type ListResponse struct {
	Files []FileInfo `json:"files"`
	Error string     `json:"error"`
}

type ContentInfo struct {
	ID         uint   `json:"id"`
	Title      string `json:"title"`
	SourceHash string `json:"source_hash"`
	CreatedAt  string `json:"created_at"`
}

type ContentsResponse struct {
	Contents []ContentInfo `json:"contents"`
	Error    string        `json:"error"`
}

func main() {
	user := flag.String("u", "", "用户名")
	pass := flag.String("p", "", "密码")
	flag.Parse()

	if *user == "" || *pass == "" {
		fmt.Println("用法: ./client -u <用户名> -p <密码>")
		os.Exit(1)
	}

	username = *user

	fmt.Printf("正在登录用户 %s...\n", username)
	if err := login(username, *pass); err != nil {
		fmt.Printf("登录失败: %v\n", err)
		fmt.Println("尝试注册新用户...")
		if err := register(username, *pass); err != nil {
			fmt.Printf("注册失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("注册成功，正在登录...")
		if err := login(username, *pass); err != nil {
			fmt.Printf("登录失败: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("✅ 登录成功！欢迎 %s\n", username)
	fmt.Println("输入 'help' 查看可用命令")
	runInteractiveShell()
}

func runInteractiveShell() {
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Printf("\n[%s@video-platform]> ", username)
		input, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				fmt.Println("\n再见！")
				break
			}
			continue
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		args := strings.Fields(input)
		cmd := strings.ToLower(args[0])

		switch cmd {
		case "help", "h":
			showHelp()
		case "upload", "up":
			if len(args) < 2 {
				fmt.Println("用法: upload <文件路径>")
				continue
			}
			cmdUpload(args[1])
		case "ls", "list":
			cmdList()
		case "contents", "ct":
			cmdContents()
		case "download", "dl":
			if len(args) < 2 {
				fmt.Println("用法: download <file_hash> [保存路径]")
				continue
			}
			savePath := ""
			if len(args) >= 3 {
				savePath = args[2]
			}
			cmdDownload(args[1], savePath)
		case "delete", "rm":
			if len(args) < 2 {
				fmt.Println("用法: delete <file_hash>")
				continue
			}
			cmdDelete(args[1])
		case "info":
			if len(args) < 2 {
				fmt.Println("用法: info <file_hash>")
				continue
			}
			cmdInfo(args[1])
		case "whoami":
			fmt.Printf("当前用户: %s\n", username)
		case "exit", "quit", "q":
			fmt.Println("再见！")
			return
		case "clear", "cls":
			fmt.Print("\033[H\033[2J")
		default:
			fmt.Printf("未知命令: %s，输入 'help' 查看帮助\n", cmd)
		}
	}
}

func showHelp() {
	fmt.Println(`
可用命令:
  help, h           显示帮助信息
  upload, up <文件>  上传文件
  ls, list          列出我的文件
  contents, ct      列出我的内容
  download, dl <hash> [路径]  下载文件
  delete, rm <hash> 删除文件
  info <hash>       查看文件详情
  whoami            显示当前用户
  clear, cls        清屏
  exit, quit, q     退出程序`)
}

func cmdUpload(filePath string) {
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		fmt.Printf("文件不存在: %s\n", filePath)
		return
	}

	fmt.Println("正在计算文件 MD5...")
	fileContent, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Printf("读取文件失败: %v\n", err)
		return
	}

	hasher := md5.New()
	hasher.Write(fileContent)
	fileHash := hex.EncodeToString(hasher.Sum(nil))
	fileName := filepath.Base(filePath)
	fileSize := int64(len(fileContent))

	fmt.Printf("文件: %s\n", fileName)
	fmt.Printf("大小: %s\n", formatSize(fileSize))
	fmt.Printf("MD5:  %s\n", fileHash)

	initResp, err := initUpload(fileHash, fileName, fileSize)
	fmt.Println("初始化上传...",initResp)
	if err != nil {
		fmt.Printf("初始化上传失败: %v\n", err)
		return
	}

	if initResp.Status == "fast_upload" {
		fmt.Printf("✅ 秒传成功！ContentID: %d\n", initResp.ContentID)
		return
	}

	file, err := os.Open(filePath)
	if err != nil {
		fmt.Printf("打开文件失败: %v\n", err)
		return
	}
	defer file.Close()

	fi, _ := file.Stat()
	totalChunks := int(fi.Size()+ChunkSize-1) / ChunkSize

	uploadedSet := make(map[int]bool)
	for _, idx := range initResp.UploadedChunks {
		uploadedSet[idx] = true
	}

	skipCount := len(initResp.UploadedChunks)
	needUpload := totalChunks - skipCount

	if initResp.Status == "resumable" && skipCount > 0 {
		fmt.Printf("🔄 断点续传：已上传 %d/%d 分片，继续上传剩余 %d 分片\n",
			skipCount, totalChunks, needUpload)
	} else {
		fmt.Printf("ContentID: %d，开始分片上传 (%d 个分片)...\n", initResp.ContentID, totalChunks)
	}

	if needUpload == 0 {
		fmt.Println("所有分片已上传，请求合并...")
		if mergeChunks(fileHash, initResp.ContentID, totalChunks, fileName, fi.Size()) {
			fmt.Println("🎉 上传成功！")
		}
		return
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 3)
	successCount := skipCount
	var mu sync.Mutex

	for i := 0; i < totalChunks; i++ {
		if uploadedSet[i] {
			continue
		}

		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			partBuffer := make([]byte, ChunkSize)
			n, err := file.ReadAt(partBuffer, int64(idx)*int64(ChunkSize))
			if n <= 0 {
				if err != nil && err != io.EOF {
					fmt.Printf("\n读取分片 %d 失败: %v\n", idx, err)
				}
				return
			}

			success := false
			for retry := 0; retry < 3; retry++ {
				if uploadChunk(fileHash, initResp.ContentID, idx, totalChunks, partBuffer[:n]) {
					success = true
					break
				}
				time.Sleep(time.Duration(retry+1) * 500 * time.Millisecond)
			}

			mu.Lock()
			if success {
				successCount++
				fmt.Printf("\r上传进度: %d/%d", successCount, totalChunks)
			}
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	fmt.Println()

	if successCount != totalChunks {
		fmt.Printf("⚠️ 上传未完成：%d/%d 分片成功，请重新运行继续上传\n", successCount, totalChunks)
		return
	}

	fmt.Println("请求合并分片...")
	if mergeChunks(fileHash, initResp.ContentID, totalChunks, fileName, fi.Size()) {
		fmt.Println("🎉 上传成功！")
	}
}

func cmdList() {
	req, _ := authRequest("GET", ServerURL+"/files", nil)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	var result ListResponse
	json.NewDecoder(resp.Body).Decode(&result)

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("获取文件列表失败: %s\n", result.Error)
		return
	}

	if len(result.Files) == 0 {
		fmt.Println("暂无文件")
		return
	}

	fmt.Println("\n我的文件:")
	fmt.Println(strings.Repeat("-", 80))
	fmt.Printf("%-4s %-20s %-34s %-10s %-8s\n", "ID", "文件名", "Hash", "大小", "状态")
	fmt.Println(strings.Repeat("-", 80))

	for _, f := range result.Files {
		status := map[int]string{0: "上传中", 1: "已完成", 2: "转码中", -1: "已取消"}[f.Status]
		if status == "" {
			status = "未知"
		}
		fmt.Printf("%-4d %-20s %-34s %-10s %-8s\n",
			f.ID, truncate(f.FileName, 18), f.FileHash, formatSize(f.FileSize), status)
	}
	fmt.Println(strings.Repeat("-", 80))
}

func cmdContents() {
	req, _ := authRequest("GET", ServerURL+"/contents", nil)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		fmt.Printf("请求失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	var result ContentsResponse
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Contents) == 0 {
		fmt.Println("暂无内容")
		return
	}

	fmt.Println("\n我的内容:")
	fmt.Println(strings.Repeat("-", 70))
	fmt.Printf("%-4s %-25s %-34s\n", "ID", "标题", "SourceHash")
	fmt.Println(strings.Repeat("-", 70))
	for _, c := range result.Contents {
		fmt.Printf("%-4d %-25s %-34s\n", c.ID, truncate(c.Title, 23), c.SourceHash)
	}
	fmt.Println(strings.Repeat("-", 70))
}

func cmdDownload(fileHash, savePath string) {
	if savePath == "" {
		savePath = "./" + fileHash
	}
	req, _ := authRequest("GET", ServerURL+"/files/"+fileHash+"/download", nil)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		fmt.Printf("下载失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("下载失败: %s\n", string(body))
		return
	}

	out, err := os.Create(savePath)
	if err != nil {
		fmt.Printf("创建文件失败: %v\n", err)
		return
	}
	defer out.Close()

	written, _ := io.Copy(out, resp.Body)
	fmt.Printf("✅ 下载完成: %s (%s)\n", savePath, formatSize(written))
}

func cmdDelete(fileHash string) {
	req, _ := authRequest("DELETE", ServerURL+"/files/"+fileHash, nil)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		fmt.Printf("删除失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		fmt.Println("✅ 删除成功")
	} else {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("删除失败: %s\n", string(body))
	}
}

func cmdInfo(fileHash string) {
	req, _ := authRequest("GET", ServerURL+"/files/"+fileHash, nil)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		fmt.Printf("获取信息失败: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Println("文件不存在或无权访问")
		return
	}

	var result FileInfo
	json.NewDecoder(resp.Body).Decode(&result)
	fmt.Printf("\n文件信息:\n  ID: %d\n  文件名: %s\n  Hash: %s\n  大小: %s\n  创建时间: %s\n",
		result.ID, result.FileName, result.FileHash, formatSize(result.FileSize), result.CreatedAt)
}

func register(user, pass string) error {
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	resp, err := http.Post(ServerURL+"/auth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var result AuthResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", result.Error)
	}
	return nil
}

func login(user, pass string) error {
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	resp, err := http.Post(ServerURL+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var result AuthResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s", result.Error)
	}
	authToken = result.Token
	return nil
}

func authRequest(method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+authToken)
	return req, nil
}

func initUpload(fileHash, fileName string, fileSize int64) (*InitResponse, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"file_hash": fileHash, "file_name": fileName, "file_size": fileSize,
	})
	req, _ := authRequest("POST", ServerURL+"/upload/init", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result InitResponse
	json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", result.Error)
	}
	return &result, nil
}

func uploadChunk(fileHash string, contentID uint, index, totalChunks int, data []byte) bool {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	writer.WriteField("file_hash", fileHash)
	writer.WriteField("content_id", strconv.Itoa(int(contentID)))
	writer.WriteField("chunk_index", strconv.Itoa(index))
	writer.WriteField("total_chunks", strconv.Itoa(totalChunks))
	part, _ := writer.CreateFormFile("chunk", fmt.Sprintf("chunk_%d", index))
	io.Copy(part, bytes.NewReader(data))
	writer.Close()

	req, _ := authRequest("POST", ServerURL+"/upload/chunk", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func mergeChunks(fileHash string, contentID uint, totalChunks int, fileName string, fileSize int64) bool {
	body, _ := json.Marshal(map[string]interface{}{
		"file_hash": fileHash, "content_id": contentID,
		"total_chunks": totalChunks, "file_name": fileName, "file_size": fileSize,
	})
	req, _ := authRequest("POST", ServerURL+"/upload/merge", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		fmt.Printf("合并失败: %v\n", err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("合并失败: %s\n", string(body))
		return false
	}
	return true
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func formatSize(size int64) string {
	switch {
	case size < 1024:
		return fmt.Sprintf("%dB", size)
	case size < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(size)/1024)
	case size < 1024*1024*1024:
		return fmt.Sprintf("%.1fMB", float64(size)/(1024*1024))
	default:
		return fmt.Sprintf("%.1fGB", float64(size)/(1024*1024*1024))
	}
}