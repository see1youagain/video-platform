package db

import (
	"fmt"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// User 用户表
type User struct {
	ID        uint      `gorm:"primaryKey"`
	Username  string    `gorm:"uniqueIndex;type:varchar(100);not null"`
	Password  string    `gorm:"not null"` // 存储 bcrypt 哈希后的字符串，不是明文
	CreatedAt time.Time
}

// 物理文件（版本）表：每个 FileMeta 是一个具体文件版本，关联到一个 Content
type FileMeta struct {
    // 使用文件内容的 MD5 作为主键（若要支持重名文件或重复文件，考虑改用复合主键或自增 ID）
    FileHash   string    `gorm:"primaryKey;type:char(32)"` // 内容 MD5
    ContentID  uint      `gorm:"index"`                     // 归属的 content
    FilePath   string    `gorm:"type:varchar(255)"`         // 存储路径
    FileSize   int64     // 字节数
    Format     string    `gorm:"type:varchar(50)"` // 容器格式，如 mp4/mkv
    VideoCodec string    `gorm:"type:varchar(50)"` // video codec, e.g. h264
    AudioCodec string    `gorm:"type:varchar(50)"` // audio codec, e.g. aac
    Bitrate    int64     // 码率（bps）
    Width      int       // 分辨率宽
    Height     int       // 分辨率高
    Duration   int       // 时长（秒）
    RefCount   int       `gorm:"default:0"` // 引用计数（UserContent 引用）
    CreatedAt  time.Time
}

// Content 表示一次上传任务/语义上的内容（多个版本/转码结果挂在同一 content 下）
type Content struct {
    ID         uint       `gorm:"primaryKey"`                     // content_id
    OwnerID    int        `gorm:"index"`                          // 上传者 user id
    SourceHash string     `gorm:"index;type:char(32);default:''"` // 上传时的源文件 hash（可为空）
    Title      string
    CreatedAt  time.Time
}

// 用户-内容视图（将用户与 content 关联）
// 一个用户可以有多个 content（每个 content 下有多个 FileMeta 版本）
type UserContent struct {
    ID        uint      `gorm:"primaryKey"`
    UserID    int       `gorm:"index"`
    ContentID uint      `gorm:"index"`
	FileName  string
    FileHash  string    // 用户给该 content 的命名或上传的原始文件名（冗余便于展示）
    Status    int       // 0: 上传中，1: 已完成，2: 转码中
    CreatedAt time.Time
    UpdatedAt time.Time
}

// 初始化数据库连接 (标准 Gorm 连接代码)
var DB *gorm.DB

func InitDB(dsn string) error {
    var err error
    DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{
        Logger: logger.Default.LogMode(logger.Info),
    })
    if err != nil {
        return fmt.Errorf("failed to connect database: %w", err)
    }
    sqlDB, err := DB.DB()
    if err != nil {
        return fmt.Errorf("failed to get db instance: %w", err)
    }
    sqlDB.SetMaxIdleConns(10)
    sqlDB.SetMaxOpenConns(100)
    sqlDB.SetConnMaxLifetime(time.Hour)

    // AutoMigrate：注意顺序，先 Content，再 FileMeta，再 UserContent
    if err := DB.AutoMigrate(&Content{}, &FileMeta{}, &UserContent{}, &User{}); err != nil {
        return fmt.Errorf("failed to migrate database: %w", err)
    }
    return nil
}

func GetDB() *gorm.DB {
    return DB
}

