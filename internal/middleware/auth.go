package middleware

import (
	"net/http"
	"strings"
	"video-platform/internal/utils"

	"github.com/gin-gonic/gin"
)

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "需要认证"})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token格式错误"})
			c.Abort()
			return
		}

		claims, err := utils.ParseToken(parts[1])
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token无效或已过期"})
			c.Abort()
			return
		}

		// 提取 user_id（JWT 中存储为 float64）
		userIDFloat, ok := claims["user_id"].(float64)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token中缺少用户信息"})
			c.Abort()
			return
		}

		c.Set("user_id", int(userIDFloat))
		c.Set("username", claims["username"])
		c.Next()
	}
}