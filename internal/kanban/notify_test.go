package kanban

import (
	"testing"
)

func TestTaskEndNotificationCallback(t *testing.T) {
	e := setupTest(t, false)
	var events []TaskNotifyEvent
	e.manager.OnTaskEnd = func(ev TaskNotifyEvent) {
		events = append(events, ev)
	}

	// 1. OneShot task finishes -> StatusDone notification
	t1 := e.createTask(DraftInput{Title: "通知一次性任务", OneShot: true})
	if _, err := e.manager.Launch(t1, "", "", ""); err != nil {
		t.Fatal(err)
	}
	e.waitTask(t1, StatusDone)
	if len(events) != 1 || events[0].TaskID != t1.ID || events[0].Status != StatusDone || events[0].Error != "" {
		t.Fatalf("expected 1 done notification, got %+v", events)
	}

	// 2. Non-OneShot task finishes turn -> StatusWaiting notification
	t2 := e.createTask(DraftInput{Title: "通知持续交流任务", OneShot: false})
	if _, err := e.manager.Launch(t2, "", "", ""); err != nil {
		t.Fatal(err)
	}
	e.waitTask(t2, StatusWaiting)
	if len(events) != 2 || events[1].TaskID != t2.ID || events[1].Status != StatusWaiting || events[1].Error != "" {
		t.Fatalf("expected waiting notification, got %+v", events)
	}

	// 3. Failed task -> StatusWaiting notification with error
	t3 := e.createTask(DraftInput{Title: "通知失败任务 [fail]", OneShot: true})
	if _, err := e.manager.Launch(t3, "", "", ""); err != nil {
		t.Fatal(err)
	}
	e.waitTask(t3, StatusWaiting)
	if len(events) != 3 || events[2].TaskID != t3.ID || events[2].Status != StatusWaiting || events[2].Error == "" {
		t.Fatalf("expected error notification, got %+v", events)
	}

	// 4. Disable notifyTaskEnd in settings -> no notification emitted
	st, _ := e.request("POST", "/settings", map[string]any{"notifyTaskEnd": false})
	if st != 200 {
		t.Fatalf("expected 200 disabling notifyTaskEnd, got %d", st)
	}
	t4 := e.createTask(DraftInput{Title: "静音任务", OneShot: true})
	if _, err := e.manager.Launch(t4, "", "", ""); err != nil {
		t.Fatal(err)
	}
	e.waitTask(t4, StatusDone)
	if len(events) != 3 {
		t.Fatalf("expected no new notifications when disabled, got %+v", events)
	}
}
