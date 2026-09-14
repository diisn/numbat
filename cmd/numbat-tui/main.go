package main

import (
	"flag"
	"log/slog"
	"os"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
)

// markdownDocumentMargin 是 glamour 默认文档样式左右边距之和（styles.defaultMargin=2）。
// 换行宽度需扣除它，渲染结果才不会超过终端宽度。
const markdownDocumentMargin = 4

// maxMarkdownRenderers 限制渲染器缓存条目数：拖动窗口会连续产生不同宽度，避免无限增长。
const maxMarkdownRenderers = 16

// markdownRenderers 按换行宽度缓存 glamour 渲染器，使 Markdown 能随终端宽度重排。
var (
	markdownMu        sync.Mutex
	markdownRenderers = map[int]*glamour.TermRenderer{}
)

// markdownRendererFor 返回按 width 换行的渲染器；宽度过窄或创建失败时返回 nil。
func markdownRendererFor(width int) *glamour.TermRenderer {
	wrapWidth := width - markdownDocumentMargin
	if wrapWidth < 1 {
		return nil
	}

	markdownMu.Lock()
	defer markdownMu.Unlock()

	if r, ok := markdownRenderers[wrapWidth]; ok {
		return r
	}
	if len(markdownRenderers) >= maxMarkdownRenderers {
		markdownRenderers = map[int]*glamour.TermRenderer{}
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(wrapWidth),
	)
	if err != nil {
		return nil
	}
	markdownRenderers[wrapWidth] = r
	return r
}

// renderMarkdown 将文本按 Markdown 渲染到指定终端宽度；失败时原样返回。
func renderMarkdown(text string, width int) string {
	r := markdownRendererFor(width)
	if r == nil {
		return text
	}
	out, err := r.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimSuffix(out, "\n")
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7438", "numbat-core gateway address (host:port)")
	flag.Parse()

	logPath := os.Getenv("NUMBAT_TUI_LOG")
	if logPath == "" {
		logPath = "numbat-tui.log"
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		slog.Warn("failed to open log file", "err", err)
	} else {
		defer f.Close()
		slog.SetDefault(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo})))
	}

	// WithMouseCellMotion 启用鼠标事件上报（滚轮/点击/拖拽），否则 viewport 收不到 MouseMsg。
	p := tea.NewProgram(initialModel(*addr), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		slog.Error("tea run error", "err", err)
		os.Exit(1)
	}
}
