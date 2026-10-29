//go:build darwin

package gui

import (
	"testing"

	"github.com/yetone/loom/internal/kanban"
)

func TestFormatTaskNotification(t *testing.T) {
	doneTitle, doneBody := formatTaskNotification(kanban.TaskNotifyEvent{
		TaskID:     "t1",
		Title:      "重构设置页",
		Status:     kanban.StatusDone,
		Tool:       kanban.ToolClaude,
		ProjectDir: "/Users/test/code/loom",
		ActiveMs:   192000,
	})
	if doneTitle != "任务已完成 · 重构设置页" {
		t.Fatalf("unexpected done title: %q", doneTitle)
	}
	if doneBody != "Claude Code · 用时 3 分 12 秒 · loom" {
		t.Fatalf("unexpected done body: %q", doneBody)
	}

	waitTitle, waitBody := formatTaskNotification(kanban.TaskNotifyEvent{
		TaskID:     "t2",
		Title:      "编写接口",
		Status:     kanban.StatusWaiting,
		Tool:       kanban.ToolCodex,
		ProjectDir: "/Users/test/code/loom",
		ActiveMs:   45000,
	})
	if waitTitle != "任务等待确认 · 编写接口" {
		t.Fatalf("unexpected waiting title: %q", waitTitle)
	}
	if waitBody != "Codex · 用时 45 秒 · loom" {
		t.Fatalf("unexpected waiting body: %q", waitBody)
	}

	errTitle, errBody := formatTaskNotification(kanban.TaskNotifyEvent{
		TaskID:     "t3",
		Title:      "异常任务",
		Status:     kanban.StatusWaiting,
		Tool:       kanban.ToolGemini,
		ProjectDir: "/Users/test/code/loom",
		ActiveMs:   3720000,
		Error:      "进程以状态 1 退出，请查看日志",
	})
	if errTitle != "任务执行异常 · 异常任务" {
		t.Fatalf("unexpected error title: %q", errTitle)
	}
	if errBody != "Gemini CLI · 用时 1 小时 2 分 · loom · 进程以状态 1 退出，请查看日志" {
		t.Fatalf("unexpected error body: %q", errBody)
	}
}
