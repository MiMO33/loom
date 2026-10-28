# AGENTS.md

## 参考项目

- Magpie 本地仓库：`/Users/qitianyuan/repo/magpie`（`~/repo/magpie`）
  - 这是 Magpie 的本地 repo，开发与设计时需参考它的设计。

## 架构与技术栈（macOS 桌面应用）

Loom 参考 Magpie 的技术栈与桌面壳设计，是一个**仅面向 macOS (`darwin`)** 的原生桌面应用：

- **后端与桌面壳**：Go (`1.26`) + [Wails v3](https://github.com/wailsapp/wails) (`github.com/wailsapp/wails/v3`) + macOS AppKit / Cocoa (`cgo`, `-mmacosx-version-min=12.0`)
  - `main.go` / `internal/cli`：统一入口，支持直接启动桌面端（`loom` / `loom.app`）、菜单栏驻留（`loom tray`）、本地服务（`loom serve`），以及内置 Agent 运行器与 Hook 子命令（`loom runner`、`loom hook`、`loom output`、`loom status`），无需外部 Node 运行时。
  - `internal/gui`：Wails v3 桌面窗口（`MacTitleBarHiddenInset` + `titlebar_darwin.go` 的原生红绿灯内嵌与 macOS 26 圆角适配）、菜单栏 `SystemTray`、Dock 点击重开窗口、`flock` 单实例锁、原生 `NSOpenPanel` 文件夹选择器与剪贴板接口。前端静态资源通过 `//go:embed assets` 内嵌到二进制中，由进程内 `http.Handler` 直接响应。
  - `internal/proc`：参考 Magpie 的 `UserPath()`，从 Finder / Dock 启动时自动探测登录 Shell（`zsh` / `bash` / `fish` / `nu`）的 `PATH`（包括 `~/.local/bin`、`/opt/homebrew/bin`、`nvm` 等）与 Agent 环境变量。
  - `internal/kanban`：看板核心存储（`~/.loom/db.json` 原子持久化，兼容自动迁移旧 `~/.agent-kanban` 数据）、Claude Code / Codex / Pi 适配器、tmux 交互与终端唤起、Git 快照/diff/提交/PR、Obsidian 归档。
- **前端界面**：React 19 + TypeScript (`src/`)，通过 Vite 构建到 `internal/gui/assets/` 并由 Go 编译期嵌入。

## 常用命令

```sh
make app     # 构建前端 + 编译 Go 二进制 + 打包 macOS loom.app
make build   # 构建前端 + 编译 ./loom 二进制
make test    # 运行 go vet ./... && go test ./...
make dev     # 开发模式运行桌面端
```
