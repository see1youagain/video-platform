package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"video-platform/internal/db"
	"video-platform/internal/handler"
	"video-platform/internal/redis"

	"github.com/gin-gonic/gin"
)

func main() {
	config := loadConfig()

	// 初始化数据库
	if err := db.InitDB(config.DBDsn); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	log.Println("Database initialized")

	// 初始化 Redis（统一入口）
	if err := redis.Init(redis.Config{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	}); err != nil {
		log.Fatalf("Failed to initialize Redis: %v", err)
	}
	defer redis.Close()
	log.Println("Redis initialized")

	// 设置 Gin
	if config.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.Default()
	registerRoutes(r)

	// HTTP 服务器
	srv := &http.Server{
		Addr:    ":" + config.Port,
		Handler: r,
	}

	go func() {
		log.Printf("Server starting on port %s", config.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	log.Println("Server stopped")
}

type Config struct {
	Env           string
	Port          string
	DBDsn         string
	RedisAddr     string
	RedisPassword string
	RedisDB       int
}

func loadConfig() Config {
	return Config{
		Env:           getEnv("ENV", "development"),
		Port:          getEnv("PORT", "8080"),
		DBDsn:         getEnv("DB_DSN", "root:@tcp(localhost:3306)/video_platform?parseTime=true"),
		RedisAddr:     getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       0,
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func registerRoutes(r *gin.Engine) {
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")
	{
		upload := api.Group("/upload")
		{
			upload.POST("/init", handler.InitUpload)
			upload.POST("/chunk", handler.UploadChunk)
			upload.POST("/merge", handler.MergeChunks)
			upload.POST("/fast", handler.FastUpload)
			upload.DELETE("/cancel", handler.CancelUpload)
		}

		files := api.Group("/files")
		{
			files.GET("", handler.ListFiles)
			files.GET("/:id", handler.GetFile)
			files.DELETE("/:id", handler.DeleteFile)
		}
	}
}
