package redis

import (
	"context"
	"errors"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
)

var (
	ErrLockNotHeld = errors.New("lock not held")
	ErrLockFailed  = errors.New("failed to acquire lock")
	ErrLockTimeout = errors.New("lock acquisition timeout")
)

// Client 全局 Redis 客户端
var Client *redis.Client

// Config Redis 配置
type Config struct {
	Addr     string
	Password string
	DB       int
}

// Init 初始化 Redis 连接
func Init(cfg Config) error {
	Client = redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return Client.Ping(ctx).Err()
}

// Close 关闭 Redis 连接
func Close() error {
	if Client != nil {
		return Client.Close()
	}
	return nil
}

// GetClient 获取 Redis 客户端
func GetClient() *redis.Client {
	return Client
}

// ======================== 分布式锁 ========================

// DistributedLock Redis 分布式锁
type DistributedLock struct {
	key        string
	value      string        // 唯一标识，用于安全释放
	ttl        time.Duration // 锁的过期时间
	retryDelay time.Duration // 重试间隔
	maxRetries int           // 最大重试次数
}

// NewLock 创建分布式锁
func NewLock(key string, ttl time.Duration) *DistributedLock {
	return &DistributedLock{
		key:        "lock:" + key,
		value:      uuid.New().String(),
		ttl:        ttl,
		retryDelay: 50 * time.Millisecond,
		maxRetries: 100, // 最多重试 100 次 (约 5 秒)
	}
}

// TryLock 尝试获取锁（非阻塞）
func (l *DistributedLock) TryLock(ctx context.Context) (bool, error) {
	ok, err := Client.SetNX(ctx, l.key, l.value, l.ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

// Lock 获取锁（阻塞，带超时）
func (l *DistributedLock) Lock(ctx context.Context) error {
	for i := 0; i < l.maxRetries; i++ {
		select {
		case <-ctx.Done():
			return ErrLockTimeout
		default:
		}

		ok, err := l.TryLock(ctx)
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		time.Sleep(l.retryDelay)
	}
	return ErrLockFailed
}

// Unlock 释放锁（使用 Lua 脚本保证原子性）
func (l *DistributedLock) Unlock(ctx context.Context) error {
	script := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("del", KEYS[1])
		else
			return 0
		end
	`
	result, err := Client.Eval(ctx, script, []string{l.key}, l.value).Int64()
	if err != nil {
		return err
	}
	if result == 0 {
		return ErrLockNotHeld
	}
	return nil
}

// Extend 续期锁
func (l *DistributedLock) Extend(ctx context.Context, ttl time.Duration) error {
	script := `
		if redis.call("get", KEYS[1]) == ARGV[1] then
			return redis.call("pexpire", KEYS[1], ARGV[2])
		else
			return 0
		end
	`
	result, err := Client.Eval(ctx, script, []string{l.key}, l.value, int64(ttl/time.Millisecond)).Int64()
	if err != nil {
		return err
	}
	if result == 0 {
		return ErrLockNotHeld
	}
	return nil
}

// WithLock 带锁执行函数（自动获取和释放锁）
func WithLock(ctx context.Context, key string, ttl time.Duration, fn func() error) error {
	lock := NewLock(key, ttl)
	if err := lock.Lock(ctx); err != nil {
		return err
	}
	defer lock.Unlock(ctx)
	return fn()
}
