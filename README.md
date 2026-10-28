# Loom

单人、本机、中文界面的 macOS Agent 任务看板桌面应用。基于 Go + Wails v3 + AppKit (`cgo`) 构建原生 macOS 桌面窗口与菜单栏图标，前端界面通过 `//go:embed` 内嵌于单一二进制中。

## 构建与启动（仅支持 macOS）

需要 macOS 12.0+、Git，以及已安装并登录的 Claude Code / Codex / Pi 中至少一个工具。交互模式需要 tmux；创建 PR 需要已登录的 GitHub CLI（`gh`）。

```sh
cd /Users/qitianyuan/code/loom
make app
open loom.app
```

也可直接运行命令行或开发模式：

```sh
./loom          # 启动桌面窗口与菜单栏图标
./loom tray     # 仅在 macOS 菜单栏启动
./loom serve    # 仅在前台运行本地 HTTP 服务 (http://127.0.0.1:4310)
make dev        # 开发模式运行
```

从 Finder 或 Dock 启动 `loom.app` 时，应用会自动读取登录 Shell 的 `PATH`（如 `~/.local/bin`、`/opt/homebrew/bin`、`nvm` 等）及 Agent 环境变量。关闭主窗口会隐藏到后台并保留菜单栏图标，正在运行的 Agent 任务不会中断；点击 Dock 图标或菜单栏图标可随时重新唤起窗口。

数据目录默认位于 `~/.loom/`（首次启动若存在旧版 `~/.agent-kanban/` 数据会自动迁移）。

## 使用

1. 新建任务，填写需求和项目目录（可直接点击「选择…」调用 macOS 原生目录选择器）。归档目录默认使用 `/tmp`，留空时也写入 `/tmp`；可按任务改为 Obsidian 笔记库中的目录。项目目录留空时，使用该任务自己的 `output/` 目录。
2. 可先放入 todo 并继续编辑，或直接运行。将 todo 卡片拖到 doing 也会启动 Agent。
3. 一次性任务默认 headless，全部正常结束后自动提交并归档。失败或中止的任务进入 waiting，等待处理。
4. 取消一次性任务后可选择 tmux。所有工作 session 停止或等待输入时，进入 waiting；任一 session 恢复运行时回到 doing。
5. 详情侧栏可追加 Agent、查看产物与日志、发送消息、查看 diff。headless 对话使用明确的原生 session ID 恢复；attach 会将已结束的 headless session 恢复到 tmux 并在 macOS Terminal 中打开。
6. waiting 中点击确认完成，检查 diff，然后选择「提交并完成」或「提交并创建 PR」。无 Git 仓库时直接确认归档。

### 工具与 Hook

| 工具 | headless | tmux / 继续交流 | 状态检测 |
| --- | --- | --- | --- |
| Claude Code | `--print --output-format stream-json` | `--resume <id>` | 本次运行专用 `--settings` 注入 `loom hook` |
| Codex | `exec --json` | `resume <id>` / `exec resume <id>` | 本次调用的 inline hooks 调用 `loom hook` |
| Pi | `--mode json --print` | 明确的 `--session` | 本次运行生成的 extension 调用 `loom hook` |

### 输出文件回报

Agent 可在 Loom 注入的任务环境中直接调用 `loom` 二进制回报输出文件：

```sh
loom output '/绝对路径/报告.md' '/绝对路径/产物.html'
```

## 本地数据

```text
~/.loom/
├── db.json
├── app-*.lock
├── tasks/<task_id>/
│   ├── input/
│   ├── output/
│   ├── prompt.md
│   ├── logs/<session_id>.log
│   ├── runtime/<session_id>/<run_id>/
│   │   ├── launch.json
│   │   ├── prompt.md
│   │   ├── events.jsonl
│   │   ├── result.json
│   │   └── final.md
│   └── pi-sessions/
└── archive/<task_id>.md
```

## 验证

```sh
make test
make app
```
