package memory

import (
	"context"
	"testing"
)

func TestInMemoryMemoryStore_PutAndGet(t *testing.T) {
	store := NewInMemoryMemoryStore()
	ctx := context.Background()

	entry := &MemoryEntry{
		Key:      "preference_language",
		Value:    "Python",
		Category: "preference",
	}

	err := store.Put(ctx, 1, entry)
	if err != nil {
		t.Fatalf("Put failed: %v", err)
	}

	got, err := store.Get(ctx, 1, "preference_language")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got == nil {
		t.Fatal("Get returned nil, expected entry")
	}
	if got.Key != "preference_language" {
		t.Errorf("Key = %q, want %q", got.Key, "preference_language")
	}
	if got.Value != "Python" {
		t.Errorf("Value = %q, want %q", got.Value, "Python")
	}
	if got.Category != "preference" {
		t.Errorf("Category = %q, want %q", got.Category, "preference")
	}
	if got.UserID != 1 {
		t.Errorf("UserID = %d, want %d", got.UserID, 1)
	}
	if got.ID <= 0 {
		t.Errorf("ID = %d, want > 0", got.ID)
	}
}

func TestInMemoryMemoryStore_Get_NotFound(t *testing.T) {
	store := NewInMemoryMemoryStore()
	ctx := context.Background()

	got, err := store.Get(ctx, 1, "nonexistent")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got != nil {
		t.Errorf("Get returned entry for nonexistent key, want nil")
	}
}

func TestInMemoryMemoryStore_Put_Overwrite(t *testing.T) {
	store := NewInMemoryMemoryStore()
	ctx := context.Background()

	entry1 := &MemoryEntry{Key: "preference_language", Value: "Python", Category: "preference"}
	store.Put(ctx, 1, entry1)

	entry2 := &MemoryEntry{Key: "preference_language", Value: "Go", Category: "preference"}
	store.Put(ctx, 1, entry2)

	got, _ := store.Get(ctx, 1, "preference_language")
	if got.Value != "Go" {
		t.Errorf("Value = %q, want %q (overwrite)", got.Value, "Go")
	}
	// ID should stay the same after overwrite
	id1, _ := store.Get(ctx, 1, "preference_language")
	entry3 := &MemoryEntry{Key: "preference_language", Value: "Java", Category: "preference"}
	store.Put(ctx, 1, entry3)
	id2, _ := store.Get(ctx, 1, "preference_language")
	if id2.ID != id1.ID {
		t.Errorf("ID changed after overwrite: %d → %d, want same", id1.ID, id2.ID)
	}
}

func TestInMemoryMemoryStore_List(t *testing.T) {
	store := NewInMemoryMemoryStore()
	ctx := context.Background()

	store.Put(ctx, 1, &MemoryEntry{Key: "preference_language", Value: "Python", Category: "preference"})
	store.Put(ctx, 1, &MemoryEntry{Key: "fact_role", Value: "后端开发者", Category: "fact"})
	store.Put(ctx, 2, &MemoryEntry{Key: "preference_language", Value: "Java", Category: "preference"})

	// List all for user 1
	entries, err := store.List(ctx, 1, "")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List for user 1 returned %d entries, want 2", len(entries))
	}

	// List by category
	preferences, err := store.List(ctx, 1, "preference")
	if err != nil {
		t.Fatalf("List by category failed: %v", err)
	}
	if len(preferences) != 1 {
		t.Fatalf("List preference for user 1 returned %d entries, want 1", len(preferences))
	}
	if preferences[0].Key != "preference_language" {
		t.Errorf("Key = %q, want %q", preferences[0].Key, "preference_language")
	}

	// List for user 2
	entries2, _ := store.List(ctx, 2, "")
	if len(entries2) != 1 {
		t.Fatalf("List for user 2 returned %d entries, want 1", len(entries2))
	}
}

func TestInMemoryMemoryStore_Delete(t *testing.T) {
	store := NewInMemoryMemoryStore()
	ctx := context.Background()

	store.Put(ctx, 1, &MemoryEntry{Key: "preference_language", Value: "Python", Category: "preference"})
	store.Delete(ctx, 1, "preference_language")

	got, _ := store.Get(ctx, 1, "preference_language")
	if got != nil {
		t.Errorf("Get after Delete returned entry, want nil")
	}
}

func TestInMemoryMemoryStore_MultiUserIsolation(t *testing.T) {
	store := NewInMemoryMemoryStore()
	ctx := context.Background()

	store.Put(ctx, 1, &MemoryEntry{Key: "preference_language", Value: "Python", Category: "preference"})
	store.Put(ctx, 2, &MemoryEntry{Key: "preference_language", Value: "Java", Category: "preference"})

	got1, _ := store.Get(ctx, 1, "preference_language")
	if got1.Value != "Python" {
		t.Errorf("User 1 preference = %q, want Python", got1.Value)
	}

	got2, _ := store.Get(ctx, 2, "preference_language")
	if got2.Value != "Java" {
		t.Errorf("User 2 preference = %q, want Java", got2.Value)
	}

	// User 2 delete should not affect user 1
	store.Delete(ctx, 2, "preference_language")
	got1After, _ := store.Get(ctx, 1, "preference_language")
	if got1After == nil {
		t.Error("User 1 entry deleted after User 2 delete")
	}
}

// ========== 注入逻辑测试 ==========

func TestBuildMemoryInjection_Empty(t *testing.T) {
	msg := BuildMemoryInjection(nil)
	if msg != nil {
		t.Error("BuildMemoryInjection(nil) returned non-nil, want nil")
	}

	msg = BuildMemoryInjection([]*MemoryEntry{})
	if msg != nil {
		t.Error("BuildMemoryInjection([]) returned non-nil, want nil")
	}
}

func TestBuildMemoryInjection_WithEntries(t *testing.T) {
	entries := []*MemoryEntry{
		{Key: "preference_language", Value: "Python", Category: "preference"},
		{Key: "fact_role", Value: "后端开发者", Category: "fact"},
		{Key: "rule_no_email_forwarding", Value: "禁止转发", Category: "rule"},
		{Key: "custom_key", Value: "custom_value", Category: "custom"},
	}

	msg := BuildMemoryInjection(entries)
	if msg == nil {
		t.Fatal("BuildMemoryInjection returned nil, want SystemMessage")
	}

	content := msg.Content
	if !contains(content, "偏好编程语言：Python") {
		t.Errorf("preference entry not found in content: %s", content)
	}
	if !contains(content, "角色：后端开发者") {
		t.Errorf("fact entry not found in content: %s", content)
	}
	if !contains(content, "规则：禁止转发") {
		t.Errorf("rule entry not found in content: %s", content)
	}
	if !contains(content, "custom_key：custom_value") {
		t.Errorf("default category entry not found in content: %s", content)
	}
}

func TestShouldSaveMemory(t *testing.T) {
	tests := []struct {
		msg      string
		expected bool
	}{
		{"请记住我喜欢Python", true},
		{"记住我是后端开发", true},
		{"我偏好简洁的回答", true},
		{"我喜欢用Go", true},
		{"我是Java开发者", true},
		{"以后都用Python", true},
		{"默认用简洁回答", true},
		{"每次都帮我写代码", true},
		{"帮我计算2+3", false},
		{"搜索文件中的关键词", false},
		{"你好", false},
	}

	for _, tt := range tests {
		got := ShouldSaveMemory(tt.msg)
		if got != tt.expected {
			t.Errorf("ShouldSaveMemory(%q) = %v, want %v", tt.msg, got, tt.expected)
		}
	}
}

func TestExtractMemoryFromConversation_Language(t *testing.T) {
	entries := ExtractMemoryFromConversation("我喜欢用Python写代码")
	if len(entries) == 0 {
		t.Fatal("no entries extracted from preference message")
	}
	found := false
	for _, e := range entries {
		if e.Key == "preference_language" && e.Value == "Python" {
			found = true
		}
	}
	if !found {
		t.Errorf("preference_language not found in extracted entries: %v", entries)
	}
}

func TestExtractMemoryFromConversation_NoTrigger(t *testing.T) {
	entries := ExtractMemoryFromConversation("帮我计算2+3")
	if len(entries) != 0 {
		t.Errorf("extracted entries from non-trigger message: %v", entries)
	}
}

func TestExtractMemoryFromConversation_Role(t *testing.T) {
	entries := ExtractMemoryFromConversation("我是后端开发")
	found := false
	for _, e := range entries {
		if e.Key == "fact_role" && e.Value == "后端开发者" {
			found = true
		}
	}
	if !found {
		t.Errorf("fact_role not found in extracted entries: %v", entries)
	}
}

func TestExtractMemoryFromConversation_Style(t *testing.T) {
	entries := ExtractMemoryFromConversation("我偏好简洁的回答")
	found := false
	for _, e := range entries {
		if e.Key == "preference_response_style" && e.Value == "简洁" {
			found = true
		}
	}
	if !found {
		t.Errorf("preference_response_style not found in extracted entries: %v", entries)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
