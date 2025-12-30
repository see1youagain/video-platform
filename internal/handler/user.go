package handler

import (
	"net/http"
	"video-platform/internal/db"
	"video-platform/internal/utils"

	"github.com/gin-gonic/gin"
)

// Register 注册接口
func Register(c *gin.Context) {
	var input struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 1. 密码加密
	hash, err := utils.HashPassword(input.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "加密失败"})
		return
	}

	// 2. 存入数据库
	user := db.User{Username: input.Username, Password: hash}
	if err := db.GetDB().Create(&user).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "用户名可能已存在"}) // 简单处理
		return
	}

	c.JSON(http.StatusOK, gin.H{"msg": "注册成功"})
}

// Login 登录接口
func Login(c *gin.Context) {
	var input struct {
		Username string `json:"username" binding:"required"`
		Password string `json:"password" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 1. 查找用户
	var user db.User
	if err := db.GetDB().Where("username = ?", input.Username).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}

	// 2. 校验密码
	if !utils.CheckPasswordHash(input.Password, user.Password) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}

	// 3. 生成 Token
	token, err := utils.GenerateToken(user.ID, user.Username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成Token失败"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"token": token})
}