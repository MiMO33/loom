//go:build darwin

package gui

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/yetone/loom/internal/kanban"
)

//go:embed tray.png
var trayIcon []byte

//go:embed icon-1024.png
var appIconLarge []byte

//go:embed assets
var assets embed.FS

func appIconFor() []byte {
	if exe, err := os.Executable(); err == nil {
		if _, err := os.Stat(filepath.Join(filepath.Dir(exe), "..", "Resources", "Assets.car")); err == nil {
			return nil
		}
	}
	return appIconLarge
}

type host struct {
	app     *application.App
	main    *application.WebviewWindow
	tray    *application.SystemTray
	manager *kanban.Manager
	closing atomic.Bool
	ready   chan struct{}
	once    sync.Once
}

func (h *host) whenReady(fn func()) {
	go func() {
		<-h.ready
		fn()
	}()
}

func (h *host) ShowMain() {
	application.InvokeSync(func() { h.openMain() })
}

func (h *host) openMain() {
	h.closing.Store(false)
	if h.main == nil {
		return
	}
	showHere(h.main)
}

func (h *host) reopenMain() {
	if h.main == nil || !windowOpen(h.main) {
		h.openMain()
		return
	}
	h.closing.Store(false)
	activateApp()
}

func (h *host) hideMain() {
	application.InvokeSync(func() {
		if h.main != nil {
			h.main.Hide()
		}
	})
}

func (h *host) leaveFullscreenThenHide() {
	h.closing.Store(true)
	application.InvokeSync(func() {
		if h.main != nil {
			h.main.UnFullscreen()
		}
	})
	time.AfterFunc(3*time.Second, func() {
		if h.closing.Swap(false) {
			application.InvokeAsync(h.hideMain)
		}
	})
}

func (h *host) ChooseFolder(title string) (string, error) {
	d := h.app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		SetTitle(title)
	if w := application.InvokeSyncWithResult(func() *application.WebviewWindow { return h.main }); w != nil {
		d.AttachToWindow(w)
	}
	return d.PromptForSingleSelection()
}

func (h *host) OpenFolder(path string) error {
	return h.app.Env.OpenFileManager(path, false)
}

func (h *host) OpenURL(url string) error {
	return h.app.Browser.OpenURL(url)
}

func (h *host) Copy(text string) bool {
	return h.app.Clipboard.SetText(text)
}

func (h *host) makeMain() *application.WebviewWindow {
	w := h.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "Loom",
		URL:              "/",
		Width:            1320,
		Height:           840,
		MinWidth:         1060,
		MinHeight:        640,
		Hidden:           true,
		BackgroundColour: application.NewRGB(250, 249, 245),
		Mac: application.MacWindow{
			TitleBar: application.MacTitleBarHiddenInset,
			CollectionBehavior: application.MacWindowCollectionBehaviorMoveToActiveSpace |
				application.MacWindowCollectionBehaviorFullScreenPrimary,
		},
	})
	w.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		if w.IsFullscreen() {
			h.leaveFullscreenThenHide()
			return
		}
		h.hideMain()
	})
	w.OnWindowEvent(events.Mac.WindowDidExitFullScreen, func(*application.WindowEvent) {
		if h.closing.Swap(false) {
			h.hideMain()
		}
	})
	return w
}

func CombinedHandler(apiHandler http.Handler) http.Handler {
	sub := staticFS()
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/api" {
			apiHandler.ServeHTTP(w, r)
			return
		}
		cleanPath := strings.TrimPrefix(filepath.Clean(r.URL.Path), "/")
		if cleanPath == "" || cleanPath == "." {
			cleanPath = "index.html"
		}
		if _, err := fs.Stat(sub, cleanPath); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		if b, err := fs.ReadFile(sub, "index.html"); err == nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	})
}

func listenLoopback() (net.Listener, int, error) {
	if p := os.Getenv("PORT"); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil || port < 1024 || port > 65535 {
			return nil, 0, fmt.Errorf("PORT 必须是 1024–65535 的整数")
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return nil, 0, err
		}
		return ln, port, nil
	}
	if ln, err := net.Listen("tcp", "127.0.0.1:4310"); err == nil {
		return ln, 4310, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, err
	}
	return ln, ln.Addr().(*net.TCPAddr).Port, nil
}

// Run starts the macOS desktop application (Wails v3 window + menu bar tray + local hook server).
func Run(version string, showMain bool) error {
	dataDir := kanban.DefaultDataDir()
	if runningAlready(dataDir) {
		return nil
	}

	ln, port, err := listenLoopback()
	if err != nil {
		return err
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	cwd, _ := os.Getwd()

	manager, err := kanban.NewManager(kanban.ManagerOptions{
		AppRoot: cwd,
		DataDir: dataDir,
		URL:     baseURL,
	})
	if err != nil {
		_ = ln.Close()
		return err
	}

	h := &host{
		manager: manager,
		ready:   make(chan struct{}),
	}
	handler := CombinedHandler(kanban.NewAPIHandler(manager, h))
	httpSrv := &http.Server{Handler: handler}
	go func() {
		_ = httpSrv.Serve(ln)
	}()
	manager.Start()

	h.app = application.New(application.Options{
		Name:        "Loom",
		Description: "macOS Agent 任务看板",
		Icon:        appIconFor(),
		Assets:      application.AssetOptions{Handler: kanban.OwnWindow(handler)},
		Mac:         application.MacOptions{ActivationPolicy: application.ActivationPolicyRegular},
		OnShutdown: func() {
			manager.Close()
			_ = httpSrv.Close()
		},
		ErrorHandler: func(err error) {
			log.Println("loom:", err)
		},
	})

	appMenu := application.DefaultApplicationMenu()
	h.app.Menu.Set(appMenu)

	h.app.Event.RegisterApplicationEventHook(events.Mac.ApplicationShouldHandleReopen, func(e *application.ApplicationEvent) {
		application.InvokeSync(h.reopenMain)
		e.Cancel()
	})

	h.main = h.makeMain()

	menu := h.app.NewMenu()
	menu.Add("打开 Loom").OnClick(func(*application.Context) { h.ShowMain() })
	statusItem := menu.Add("状态：空闲").SetEnabled(false)
	menu.AddSeparator()
	menu.Add("版本 " + version).SetEnabled(false)
	menu.Add("退出 Loom").OnClick(func(*application.Context) { h.app.Quit() })

	h.tray = h.app.SystemTray.New()
	h.tray.SetTooltip("Loom · Agent 任务看板")
	h.tray.SetTemplateIcon(trayIcon)
	h.tray.SetMenu(menu)
	h.tray.OnClick(func() {
		h.ShowMain()
	})

	go func() {
		<-h.ready
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		lastLabel := ""
		for range ticker.C {
			manager.Lock()
			doing, waiting := 0, 0
			for _, t := range manager.Store.Data.Tasks {
				switch t.Status {
				case kanban.StatusDoing:
					doing++
				case kanban.StatusWaiting:
					waiting++
				}
			}
			manager.Unlock()
			label := "状态：空闲"
			if doing > 0 || waiting > 0 {
				label = fmt.Sprintf("执行中 %d · 待确认 %d", doing, waiting)
			}
			if label != lastLabel {
				lastLabel = label
				l := label
				application.InvokeAsync(func() {
					statusItem.SetLabel(l)
					menu.Update()
				})
			}
		}
	}()

	h.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		plainTitlebar(h.main)
		h.once.Do(func() { close(h.ready) })
	})

	if showMain {
		h.whenReady(func() { h.ShowMain() })
	}

	return h.app.Run()
}

// RunServe runs only the loopback HTTP server in the foreground (for headless/CLI server mode).
func RunServe() error {
	dataDir := kanban.DefaultDataDir()
	ln, port, err := listenLoopback()
	if err != nil {
		return err
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	cwd, _ := os.Getwd()
	manager, err := kanban.NewManager(kanban.ManagerOptions{
		AppRoot: cwd,
		DataDir: dataDir,
		URL:     baseURL,
	})
	if err != nil {
		_ = ln.Close()
		return err
	}
	defer manager.Close()
	manager.Start()
	fmt.Printf("Loom：%s\n数据目录：%s\n", baseURL, dataDir)
	handler := CombinedHandler(kanban.NewAPIHandler(manager, nil))
	return http.Serve(ln, handler)
}
