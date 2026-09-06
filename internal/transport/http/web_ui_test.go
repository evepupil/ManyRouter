package httptransport

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
)

func TestWebUIProvidesAssetsAndSPAFallbackWithoutMaskingAPI404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	assets := fstest.MapFS{
		"index.html":         &fstest.MapFile{Data: []byte("<main>console</main>")},
		"static/app.js":      &fstest.MapFile{Data: []byte("console.log('ready')")},
		"static/ignored.txt": &fstest.MapFile{Data: []byte("ignored")},
	}
	registerWebUI(router, fs.FS(assets))

	asset := httptest.NewRecorder()
	router.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/static/app.js", nil))
	if asset.Code != http.StatusOK || asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset response = %d headers=%v", asset.Code, asset.Header())
	}

	page := httptest.NewRecorder()
	router.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/runtime-health", nil))
	if page.Code != http.StatusOK || page.Body.String() != "<main>console</main>" {
		t.Fatalf("page response = %d body=%s", page.Code, page.Body.String())
	}

	api := httptest.NewRecorder()
	router.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil))
	if api.Code != http.StatusNotFound || api.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("API response = %d headers=%v body=%s", api.Code, api.Header(), api.Body.String())
	}
}
