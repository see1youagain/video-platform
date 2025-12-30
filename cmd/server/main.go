package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"video-platform/internal/db"
	"video-platform/internal/handler"
	"video-platform/internal/logic"
	"video-platform/internal/middleware"
	"video-platform/internal/redis"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	config := loadConfig()

	// 初始化数据库
	if err := db.InitDB(config.DBDsn); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	log.Println("Database initialized")

	// 初始化 Redis
	if err := redis.Init(redis.Config{
		Addr:     config.RedisAddr,
		Password: config.RedisPassword,
		DB:       config.RedisDB,
	}); err != nil {
		log.Fatalf("Failed to initialize Redis: %v", err)
	}
	defer redis.Close()
	log.Println("Redis initialized")

	// 🔥 启动时从数据库加载墓碑到 Redis
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	if err := logic.LoadTombstonesOnStartup(ctx); err != nil {
		log.Printf("Warning: Failed to load tombstones: %v", err)
		// 不致命，继续启动
	}
	cancel()

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

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	srv.Shutdown(shutdownCtx)
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
        Env:  getEnv("ENV", "development"),
        Port: getEnv("PORT", "8080"),
        // 优先使用完整 DB_DSN；否则从单独变量组装
        DBDsn: func() string {
            if dsn := os.Getenv("DB_DSN"); dsn != "" {
                return dsn
            }
            user := getEnv("DB_USER", "")
            pass := getEnv("DB_PASS", "")
            host := getEnv("DB_HOST", "127.0.0.1")
            port := getEnv("DB_PORT", "3306")
            name := getEnv("DB_NAME", "videodb")
            return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
                user, pass, host, port, name)
        }(),
        RedisAddr:     getEnv("REDIS_ADDR", "127.0.0.1:6379"),
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
		// 公开路由（无需认证）
		auth := api.Group("/auth")
		{
			auth.POST("/register", handler.Register)
			auth.POST("/login", handler.Login)
		}

		// 需要认证的路由
		protected := api.Group("")
		protected.Use(middleware.AuthMiddleware())
		{
			upload := protected.Group("/upload")
			{
				upload.POST("/init", handler.InitUpload)
				upload.POST("/chunk", handler.UploadChunk)
				upload.POST("/merge", handler.MergeChunks)
				upload.POST("/fast", handler.FastUpload)
				upload.DELETE("/cancel", handler.CancelUpload)
			}

			files := protected.Group("/files")
			{
				files.GET("", handler.ListFiles)
				files.GET("/:id", handler.GetFile)
				files.DELETE("/:id", handler.DeleteFile)
			}

			contents := protected.Group("/contents")
			{
				contents.GET("", handler.ListContents)
				contents.GET("/:id", handler.GetContent)
			}
		}
	}
}
