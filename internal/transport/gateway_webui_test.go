package transport

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// testWebUI 构造一份最小的前端产物用于测试（不依赖真实构建）。
func testWebUI() fstest.MapFS {
	return fstest.MapFS{
		"index.html":          &fstest.MapFile{Data: []byte("<!doctype html><div id=root></div>")},
		"assets/index-abc.js": &fstest.MapFile{Data: []byte("console.log(1)")},
	}
}

func TestGatewayWebUI(t *testing.T) {
	g := NewGateway(":0")
	g.SetWebUI(testWebUI())
	server := httptest.NewServer(g.router)
	defer server.Close()

	cases := []struct {
		name        string
		path        string
		wantStatus  int
		wantBody    string
		wantNoCache string
	}{
		{"/app 入口", "/app", http.StatusOK, "<div id=root>", "no-cache"},
		{"/app/ 入口", "/app/", http.StatusOK, "<div id=root>", "no-cache"},
		{"/app/index.html", "/app/index.html", http.StatusOK, "<div id=root>", "no-cache"},
		{"/app 下的静态资源", "/app/assets/index-abc.js", http.StatusOK, "console.log(1)", "immutable"},
		// SPA 回退：前端路由路径应返回入口页
		{"/app 未命中路径回退", "/app/some/client/route", http.StatusOK, "<div id=root>", "no-cache"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, err := http.Get(server.URL + c.path)
			if err != nil {
				t.Fatalf("GET %s failed: %v", c.path, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != c.wantStatus {
				t.Fatalf("GET %s: want status %d, got %d", c.path, c.wantStatus, resp.StatusCode)
			}
			body, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(body), c.wantBody) {
				t.Errorf("GET %s: body %q 不含 %q", c.path, string(body), c.wantBody)
			}
			if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, c.wantNoCache) {
				t.Errorf("GET %s: Cache-Control = %q, 期望包含 %q", c.path, cc, c.wantNoCache)
			}
		})
	}
}

// 未设置前端产物时 /app/* 返回 404，且不影响既有接口。
func TestGatewayWebUIWithoutAssets(t *testing.T) {
	g := NewGateway(":0")
	g.SetRPCServer(NewServer(":0"))
	server := httptest.NewServer(g.router)
	defer server.Close()

	resp, err := http.Get(server.URL + "/app/")
	if err != nil {
		t.Fatalf("GET /app/ failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("未设置产物时 GET /app/: want 404, got %d", resp.StatusCode)
	}

	// 其余接口不受影响
	health, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health failed: %v", err)
	}
	defer health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Errorf("GET /health: want 200, got %d", health.StatusCode)
	}
}

// 路径穿越不应读到产物以外的内容。
// 直接构造请求，避免 http.Client 在发送前归一化掉 ".."。
func TestGatewayWebUITraversalFallsBackToIndex(t *testing.T) {
	g := NewGateway(":0")
	g.SetWebUI(testWebUI())

	for _, raw := range []string{"/app/../../etc/passwd", "/app/..%2f..%2fetc/passwd"} {
		req := httptest.NewRequest(http.MethodGet, "/app/", nil)
		req.URL.Path = raw
		rec := httptest.NewRecorder()

		g.handleWebUI(rec, req)

		body := rec.Body.String()
		if strings.Contains(body, "root:") {
			t.Fatalf("疑似读取到产物外文件 (path=%s): %s", raw, body)
		}
		// 非法路径回退到入口页
		if rec.Code != http.StatusOK || !strings.Contains(body, "<div id=root>") {
			t.Errorf("path=%s 应回退到入口页，got status=%d body=%q", raw, rec.Code, body)
		}
	}
}
