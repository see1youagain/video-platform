package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// SetupWebRoutes 设置 Web 路由
func SetupWebRoutes(r *gin.Engine, staticPath, templatesPath string) {
	// 加载 HTML 模板
	r.LoadHTMLGlob(templatesPath + "/*")

	// 静态文件
	r.Static("/static", staticPath)

	// 页面路由
	r.GET("/", indexPage)
	r.GET("/login", loginPage)
	r.GET("/upload", uploadPage)
	r.GET("/files", filesPage)
	r.GET("/play/:hash", playPage)
}

func indexPage(c *gin.Context) {
	c.HTML(http.StatusOK, "index.html", gin.H{
		"title": "视频平台",
	})
}

func loginPage(c *gin.Context) {
	c.HTML(http.StatusOK, "login.html", gin.H{
		"title": "登录/注册",
	})
}

func uploadPage(c *gin.Context) {
	c.HTML(http.StatusOK, "upload.html", gin.H{
		"title": "上传视频",
	})
}

func filesPage(c *gin.Context) {
	c.HTML(http.StatusOK, "files.html", gin.H{
		"title": "我的文件",
	})
}

func playPage(c *gin.Context) {
	hash := c.Param("hash")
	c.HTML(http.StatusOK, "play.html", gin.H{
		"title":    "视频播放",
		"fileHash": hash,
	})
}
