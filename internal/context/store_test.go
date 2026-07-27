package context

import (
	"context"
	"testing"
)

func TestMemoryMessageStore_GetEmpty(t *testing.T) {
	store := NewMemoryMessageStore()
	msgs, err := store.Get(context.Background(), "nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msgs != nil {
		t.Errorf("expected nil for nonexistent thread, got %d messages", len(msgs))
	}
}

func TestMemoryMessageStore_AppendAndGet(t *testing.T) {
	store := NewMemoryMessageStore()
	threadID := "thread-1"

	err := store.Append(context.Background(), threadID, userMsg("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = store.Append(context.Background(), threadID, assistantMsg("hi"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs, err := store.Get(context.Background(), threadID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "hello" {
		t.Errorf("expected first msg 'hello', got '%s'", msgs[0].Content)
	}
	if msgs[1].Content != "hi" {
		t.Errorf("expected second msg 'hi', got '%s'", msgs[1].Content)
	}
}

func TestMemoryMessageStore_GetReturnsCopy(t *testing.T) {
	store := NewMemoryMessageStore()
	threadID := "thread-1"
	store.Append(context.Background(), threadID, userMsg("original"))

	msgs, _ := store.Get(context.Background(), threadID)
	msgs[0] = assistantMsg("modified") // modify returned slice

	msgs2, _ := store.Get(context.Background(), threadID)
	if msgs2[0].Content != "original" {
		t.Error("Get should return a defensive copy, internal state was modified")
	}
}

func TestMemoryMessageStore_Clear(t *testing.T) {
	store := NewMemoryMessageStore()
	threadID := "thread-1"
	store.Append(context.Background(), threadID, userMsg("hello"))

	err := store.Clear(context.Background(), threadID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	msgs, _ := store.Get(context.Background(), threadID)
	if msgs != nil {
		t.Errorf("expected nil after clear, got %d messages", len(msgs))
	}
}

func TestMemoryMessageStore_SeparateThreads(t *testing.T) {
	store := NewMemoryMessageStore()
	store.Append(context.Background(), "t1", userMsg("msg-t1"))
	store.Append(context.Background(), "t2", userMsg("msg-t2"))

	msgs1, _ := store.Get(context.Background(), "t1")
	msgs2, _ := store.Get(context.Background(), "t2")
	if len(msgs1) != 1 || msgs1[0].Content != "msg-t1" {
		t.Error("thread t1 data incorrect")
	}
	if len(msgs2) != 1 || msgs2[0].Content != "msg-t2" {
		t.Error("thread t2 data incorrect")
	}
}
