package transport

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// webUI 持有前端静态产物及其文件服务。
type webUI struct {
	fsys    fs.FS
	handler http.Handler
}

// Gateway 是 numbat-core 对外的唯一入口：HTTP 辅助路由（/health /metrics /app/*）
// 与 WebSocket JSON-RPC（/ws，经 handleWSConn 复用 Server 的 dispatch）。
type Gateway struct {
	addr      string
	server    *http.Server
	router    *gin.Engine
	started   time.Time
	mu        sync.RWMutex
	rpc       *Server
	webui     *webUI
	upgrader  websocket.Upgrader
	rateLimit float64
	rateBurst float64
	runCtx    context.Context // Run 时设置；WS 连接生命周期与之关联
}

// NewGateway 创建 HTTP 网关。
func NewGateway(addr string) *Gateway {
	g := &Gateway{
		addr:    addr,
		started: time.Now(),
	}
	g.router = g.routes()
	g.server = &http.Server{
		Addr:    addr,
		Handler: g.router,
		// WriteTimeout 置零，避免影响 /ws 长连接。
		WriteTimeout: 0,
	}
	g.upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true }, // 默认放行；生产环境应通过 SetAllowedOrigins 限制
	}
	return g
}

// SetRPCServer 设置处理 WebSocket JSON-RPC 请求的 RPC 服务器。
func (g *Gateway) SetRPCServer(s *Server) {
	g.mu.Lock()
	g.rpc = s
	g.mu.Unlock()
}

// SetRateLimit 配置单连接限流；rate 为每秒消息数，burst 为突发上限。
// rate <= 0 时禁用限流。
func (g *Gateway) SetRateLimit(rate, burst float64) {
	g.mu.Lock()
	g.rateLimit = rate
	g.rateBurst = burst
	g.mu.Unlock()
}

// SetAllowedOrigins 配置 WebSocket 允许的 Origin 列表；空列表表示放行所有（本地默认）。
func (g *Gateway) SetAllowedOrigins(origins []string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(origins) == 0 {
		g.upgrader.CheckOrigin = func(r *http.Request) bool { return true }
		return
	}
	set := make(map[string]bool, len(origins))
	for _, o := range origins {
		set[o] = true
	}
	g.upgrader.CheckOrigin = func(r *http.Request) bool {
		return set[r.Header.Get("Origin")]
	}
}

// SetWebUI 设置前端静态产物（通常来自 internal/webui 的内嵌 FS），
// 挂载在 /app/* 下。传 nil 表示不提供前端页面。
func (g *Gateway) SetWebUI(fsys fs.FS) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if fsys == nil {
		g.webui = nil
		return
	}
	g.webui = &webUI{fsys: fsys, handler: http.FileServerFS(fsys)}
}

// handleWebUI 提供 /app/* 下的前端页面与静态资源。
// 未命中的路径回退到 index.html，交由前端路由处理（SPA）。
func (g *Gateway) handleWebUI(w http.ResponseWriter, r *http.Request) {
	g.mu.RLock()
	ui := g.webui
	g.mu.RUnlock()

	if ui == nil {
		http.Error(w, "webui not available: 未内嵌前端产物", http.StatusNotFound)
		return
	}

	rel := strings.TrimPrefix(r.URL.Path, "/app")
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		rel = "index.html"
	}
	// 路径不存在、非法或指向目录时回退到入口页，由前端路由接管（SPA）。
	// fs.Stat 对含 ".." 等非法路径会直接报错，天然阻断路径穿越。
	if info, err := fs.Stat(ui.fsys, rel); err != nil || info.IsDir() {
		rel = "index.html"
	}

	// 入口页直接写字节：http.FileServer 会把 /index.html 重定向到 ./，
	// 而我们已重写 URL，会形成重定向死循环。
	if rel == "index.html" {
		data, err := fs.ReadFile(ui.fsys, "index.html")
		if err != nil {
			http.Error(w, "webui 未构建：缺少 index.html", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
		return
	}

	// 带内容哈希的资源可长期缓存
	if strings.HasPrefix(rel, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}

	// 复用同一文件服务，需把 URL 重写为产物内相对路径
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/" + rel
	ui.handler.ServeHTTP(w, r2)
}

func (g *Gateway) routes() *gin.Engine {
	r := gin.New()
	// 自定义空日志中间件：网关日志走 slog，避免 gin 默认终端彩色日志
	r.Use(gin.LoggerWithWriter(io.Discard), gin.Recovery())
	r.GET("/health", g.handleHealth)
	r.GET("/metrics", g.handleMetrics)
	r.GET("/ws", g.handleWebSocket)
	r.Any("/app", gin.WrapH(http.HandlerFunc(g.handleWebUI)))
	r.Any("/app/*path", gin.WrapH(http.HandlerFunc(g.handleWebUI)))
	return r
}

func (g *Gateway) handleHealth(c *gin.Context) {
	g.json(c.Writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (g *Gateway) handleMetrics(c *gin.Context) {
	g.mu.RLock()
	uptime := time.Since(g.started)
	rpc := g.rpc
	g.mu.RUnlock()
	metrics := map[string]any{
		"uptime":     uptime.String(),
		"goroutines": runtime.NumGoroutine(),
	}
	if rpc != nil {
		rpc.mu.Lock()
		metrics["subscribers"] = len(rpc.subscribers)
		rpc.mu.Unlock()
	}
	g.json(c.Writer, http.StatusOK, metrics)
}

func (g *Gateway) handleWebSocket(c *gin.Context) {
	g.mu.RLock()
	rpc := g.rpc
	upgrader := g.upgrader
	rateLimit := g.rateLimit
	rateBurst := g.rateBurst
	runCtx := g.runCtx
	g.mu.RUnlock()
	if rpc == nil {
		c.String(http.StatusServiceUnavailable, "rpc server not configured")
		return
	}
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err)
		return
	}
	var limiter *rateLimiter
	if rateLimit > 0 {
		if rateBurst < 1 {
			rateBurst = 1
		}
		limiter = newRateLimiter(rateLimit, rateBurst)
	}
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	if runCtx != nil {
		// 关联网关生命周期：请求 ctx 只覆盖客户端断开，core 停止时需主动取消连接。
		go func() {
			select {
			case <-runCtx.Done():
				cancel()
			case <-ctx.Done():
			}
		}()
	}
	handleWSConn(ctx, rpc, ws, limiter)
}

func (g *Gateway) json(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Run 启动网关并阻塞直到 ctx 取消或监听失败。
func (g *Gateway) Run(ctx context.Context) error {
	g.mu.Lock()
	g.runCtx = ctx
	g.mu.Unlock()

	slog.Info("gateway listening", "addr", g.addr)

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = g.server.Shutdown(shutdownCtx)
	}()

	err := g.server.ListenAndServe()
	if ctx.Err() == nil {
		// 非取消导致的退出（如端口占用）：直接返回监听错误。
		return err
	}
	// 优雅关闭：等 Shutdown 完成后，排空 in-flight WS 请求。
	<-shutdownDone
	g.mu.RLock()
	rpc := g.rpc
	g.mu.RUnlock()
	if rpc != nil {
		rpc.waitConns(10 * time.Second)
	}
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}
