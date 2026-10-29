//go:build darwin

package gui

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/yetone/loom/internal/kanban"
)

type notifier struct {
	once    sync.Once
	ns      *notifications.NotificationService
	err     error
	h       *host
	refused atomic.Bool
}

func (n *notifier) start() error {
	n.once.Do(func() {
		ns := notifications.New()
		if err := application.InvokeSyncWithError(func() error {
			return ns.ServiceStartup(context.Background(), application.ServiceOptions{})
		}); err != nil {
			n.err = err
			log.Println("notifications:", err)
			return
		}
		ns.OnNotificationResponse(func(r notifications.NotificationResult) {
			taskID, _ := r.Response.UserInfo["taskId"].(string)
			n.h.whenReady(func() {
				application.InvokeAsync(func() {
					n.h.ShowTask(taskID)
				})
			})
		})
		n.ns = ns
	})
	return n.err
}

func (n *notifier) allowed() bool {
	if n.start() != nil {
		return false
	}
	if ok, err := n.ns.CheckNotificationAuthorization(); err == nil && ok {
		return true
	}
	ok, err := n.ns.RequestNotificationAuthorization()
	n.refused.Store(err == nil && !ok)
	return err == nil && ok
}

func (n *notifier) problem() string {
	select {
	case <-n.h.ready:
	default:
		return ""
	}
	if n.start() != nil {
		return "unavailable"
	}
	if !n.refused.Load() {
		return ""
	}
	if ok, err := n.ns.CheckNotificationAuthorization(); err == nil && !ok {
		return "denied"
	}
	return ""
}

func (n *notifier) sendTaskEnd(ev kanban.TaskNotifyEvent) {
	if !n.allowed() {
		return
	}
	title, body := formatTaskNotification(ev)
	opts := notifications.NotificationOptions{
		ID:    fmt.Sprintf("loom-task-%s-%d", ev.TaskID, time.Now().UnixNano()),
		Title: title,
		Body:  body,
		Data:  map[string]any{"taskId": ev.TaskID},
	}
	if err := n.ns.SendNotification(opts); err != nil {
		log.Println("notifications:", err)
	}
}

func formatTaskNotification(ev kanban.TaskNotifyEvent) (title, body string) {
	switch {
	case ev.Status == kanban.StatusDone:
		title = "任务已完成"
	case ev.Error != "":
		title = "任务执行异常"
	default:
		title = "任务等待确认"
	}
	if ev.Title != "" {
		title += " · " + ev.Title
	}

	var parts []string
	if toolName := kanban.ToolDisplayName(ev.Tool); toolName != "" {
		parts = append(parts, toolName)
	}
	if ev.ActiveMs > 0 {
		parts = append(parts, "用时 "+formatNotifyDuration(ev.ActiveMs))
	}
	if ev.ProjectDir != "" {
		if base := kanban.ShortFolderName(ev.ProjectDir); base != "" {
			parts = append(parts, base)
		}
	}
	body = joinMiddleDot(parts)
	if ev.Error != "" {
		if body != "" {
			body += " · " + ev.Error
		} else {
			body = ev.Error
		}
	}
	if body == "" {
		body = ev.Title
	}
	return title, body
}

func formatNotifyDuration(ms int64) string {
	s := int((time.Duration(ms) * time.Millisecond).Round(time.Second) / time.Second)
	if s < 0 {
		s = 0
	}
	h, m, sec := s/3600, s/60%60, s%60
	switch {
	case h > 0:
		return fmt.Sprintf("%d 小时 %d 分", h, m)
	case m > 0:
		return fmt.Sprintf("%d 分 %d 秒", m, sec)
	default:
		return fmt.Sprintf("%d 秒", sec)
	}
}

func joinMiddleDot(parts []string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += " · "
		}
		out += p
	}
	return out
}
