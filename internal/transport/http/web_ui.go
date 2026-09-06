package httptransport

import (
	"embed"
	"io/fs"
	stdhttp "net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
)

//go:embed ui_dist
var embeddedUI embed.FS

func RegisterWebUI(router *gin.Engine) bool {
	assets, err := fs.Sub(embeddedUI, "ui_dist")
	if err != nil {
		return false
	}
	if _, err := fs.Stat(assets, "index.html"); err != nil {
		return false
	}
	registerWebUI(router, assets)
	return true
}

func registerWebUI(router *gin.Engine, assets fs.FS) {
	files := stdhttp.FileServer(stdhttp.FS(assets))
	router.NoRoute(func(c *gin.Context) {
		requestPath := strings.TrimPrefix(c.Request.URL.Path, "/")
		if requestPath == "api" || strings.HasPrefix(requestPath, "api/") || requestPath == "metrics" {
			writeError(c, stdhttp.StatusNotFound, "not_found", "请求的接口不存在")
			return
		}
		cleaned := path.Clean(requestPath)
		if cleaned != "." && fs.ValidPath(cleaned) {
			if info, err := fs.Stat(assets, cleaned); err == nil && !info.IsDir() {
				if strings.HasPrefix(cleaned, "static/") {
					c.Header("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(c.Writer, c.Request)
				return
			}
		}
		index, err := fs.ReadFile(assets, "index.html")
		if err != nil {
			writeError(c, stdhttp.StatusNotFound, "not_found", "页面不存在")
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Data(stdhttp.StatusOK, "text/html; charset=utf-8", index)
	})
}
