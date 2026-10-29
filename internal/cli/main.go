//go:build darwin

package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/yetone/loom/internal/gui"
	"github.com/yetone/loom/internal/kanban"
	"github.com/yetone/loom/internal/proc"
)

func Main(version string) {
	args := os.Args[1:]
	// Filter out macOS LaunchServices -psn_* arguments when launched from Finder/Dock.
	var filtered []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-psn_") {
			filtered = append(filtered, a)
		}
	}
	args = filtered

	if len(args) > 0 {
		switch args[0] {
		case "runner":
			if len(args) < 2 {
				fmt.Fprintln(os.Stderr, "usage: loom runner <launch.json>")
				os.Exit(2)
			}
			os.Exit(kanban.RunRunner(args[1]))
		case "hook":
			os.Exit(kanban.RunHook(args[1:]))
		case "output", "status":
			os.Exit(kanban.RunCLICommand(args[0], args[1:]))
		case "fixture-agent":
			os.Exit(kanban.RunFixtureAgent(args[1:]))
		case "version", "--version", "-v":
			fmt.Println("loom " + version)
			return
		case "serve", "web":
			proc.UserPath()
			if err := gui.RunServe(version); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "tray":
			proc.UserPath()
			if err := gui.Run(version, false); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "app", "gui":
			// Fall through to launch desktop window
		case "help", "--help", "-h":
			fmt.Println(`Loom — macOS Agent 任务看板

用法：
  loom                       启动 macOS 桌面应用
  loom tray                  仅启动菜单栏图标
  loom serve                 仅在前台运行本地 HTTP 服务
  loom output <文件路径...>  在 Agent 任务环境中回报产物文件
  loom version               显示版本`)
			return
		}
	}

	proc.UserPath()
	if err := gui.Run(version, true); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
