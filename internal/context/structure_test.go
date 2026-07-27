package context

import (
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestAnalyzeMessageStructure_Empty(t *testing.T) {
	structure := analyzeMessageStructure(nil)
	if len(structure.systemIndices) != 0 {
		t.Errorf("expected empty systemIndices, got %v", structure.systemIndices)
	}
	if len(structure.nonSystemGroups) != 0 {
		t.Errorf("expected empty nonSystemGroups, got %d groups", len(structure.nonSystemGroups))
	}
	if len(structure.pairBoundaries) != 0 {
		t.Errorf("expected empty pairBoundaries, got %v", structure.pairBoundaries)
	}
}

func TestAnalyzeMessageStructure_SystemOnly(t *testing.T) {
	msgs := []*schema.Message{sysMsg("sys1"), sysMsg("sys2")}
	structure := analyzeMessageStructure(msgs)

	if len(structure.systemIndices) != 2 {
		t.Fatalf("expected 2 systemIndices, got %d", len(structure.systemIndices))
	}
	if structure.systemIndices[0] != 0 || structure.systemIndices[1] != 1 {
		t.Errorf("expected systemIndices [0,1], got %v", structure.systemIndices)
	}
	if len(structure.nonSystemGroups) != 0 {
		t.Errorf("expected 0 nonSystemGroups, got %d", len(structure.nonSystemGroups))
	}
}

func TestAnalyzeMessageStructure_PlainConversation(t *testing.T) {
	msgs := []*schema.Message{
		userMsg("hello"),
		assistantMsg("hi"),
		userMsg("how are you"),
		assistantMsg("fine"),
	}
	structure := analyzeMessageStructure(msgs)

	if len(structure.systemIndices) != 0 {
		t.Errorf("expected no systemIndices, got %v", structure.systemIndices)
	}
	if len(structure.nonSystemGroups) != 4 {
		t.Fatalf("expected 4 nonSystemGroups, got %d", len(structure.nonSystemGroups))
	}
	for i, g := range structure.nonSystemGroups {
		if g.isPaired {
			t.Errorf("group %d should not be paired", i)
		}
		if g.count() != 1 {
			t.Errorf("group %d should have 1 message, got %d", i, g.count())
		}
	}
}

func TestAnalyzeMessageStructure_ToolCallPair(t *testing.T) {
	msgs := []*schema.Message{
		toolCallMsg("calc", `{"expr":"1+1"}`, "call_1"),
		toolResultMsg("2", "call_1"),
		toolResultMsg("extra", "call_1"),
	}
	structure := analyzeMessageStructure(msgs)

	if len(structure.nonSystemGroups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(structure.nonSystemGroups))
	}
	g := structure.nonSystemGroups[0]
	if !g.isPaired {
		t.Error("expected paired group")
	}
	if g.count() != 3 {
		t.Errorf("expected 3 messages in pair, got %d", g.count())
	}
}

func TestAnalyzeMessageStructure_MultiplePairsInterleaved(t *testing.T) {
	msgs := []*schema.Message{
		userMsg("q1"),
		toolCallMsg("calc", `{}`, "c1"),
		toolResultMsg("r1", "c1"),
		userMsg("q2"),
		toolCallMsg("calc", `{}`, "c2"),
		toolResultMsg("r2", "c2"),
	}
	structure := analyzeMessageStructure(msgs)

	if len(structure.nonSystemGroups) != 4 {
		t.Fatalf("expected 4 groups (2 independent + 2 paired), got %d", len(structure.nonSystemGroups))
	}
	// group 0: user "q1" (independent)
	if structure.nonSystemGroups[0].isPaired {
		t.Error("group 0 should be independent")
	}
	// group 1: assistant+tool pair for c1
	if !structure.nonSystemGroups[1].isPaired {
		t.Error("group 1 should be paired")
	}
	// group 2: user "q2" (independent)
	if structure.nonSystemGroups[2].isPaired {
		t.Error("group 2 should be independent")
	}
	// group 3: assistant+tool pair for c2
	if !structure.nonSystemGroups[3].isPaired {
		t.Error("group 3 should be paired")
	}
}

func TestAnalyzeMessageStructure_SystemInterspersed(t *testing.T) {
	msgs := []*schema.Message{
		sysMsg("sys1"),                    // index 0
		userMsg("q1"),                     // index 1
		toolCallMsg("calc", `{}`, "c1"),   // index 2
		toolResultMsg("r1", "c1"),         // index 3
		sysMsg("sys2"),                    // index 4
		userMsg("q2"),                     // index 5
	}
	structure := analyzeMessageStructure(msgs)

	if len(structure.systemIndices) != 2 {
		t.Fatalf("expected 2 systemIndices, got %d", len(structure.systemIndices))
	}
	if structure.systemIndices[0] != 0 || structure.systemIndices[1] != 4 {
		t.Errorf("expected systemIndices [0,4], got %v", structure.systemIndices)
	}
	// pairBoundary should map index 2 -> 3
	if structure.pairBoundaries[2] != 3 {
		t.Errorf("expected pairBoundary[2]=3, got %d", structure.pairBoundaries[2])
	}
}

func TestSeparateSystemMessages(t *testing.T) {
	msgs := []*schema.Message{
		sysMsg("sys1"),
		userMsg("q1"),
		sysMsg("sys2"),
		assistantMsg("a1"),
	}
	sysMsgs, nonSysMsgs := separateSystemMessages(msgs)

	if len(sysMsgs) != 2 {
		t.Fatalf("expected 2 system msgs, got %d", len(sysMsgs))
	}
	if len(nonSysMsgs) != 2 {
		t.Fatalf("expected 2 non-system msgs, got %d", len(nonSysMsgs))
	}
	if sysMsgs[0].Content != "sys1" || sysMsgs[1].Content != "sys2" {
		t.Errorf("system messages content mismatch")
	}
	if nonSysMsgs[0].Content != "q1" || nonSysMsgs[1].Content != "a1" {
		t.Errorf("non-system messages content mismatch")
	}
}
