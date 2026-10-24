package usecases_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/TheFranconianCoder/thought-keeper/internal/core/usecases/interfaces"
)

// fakeSourceLog is the per-source log as a test sees it: the lines that were appended, and the
// sources whose log was removed.
type fakeSourceLog struct {
	mu      sync.Mutex
	lines   []string
	removed []string
}

func (f *fakeSourceLog) Append(_ context.Context, hash string, e interfaces.SourceLogEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(e.Phase + " " + e.Level + " " + e.Message)
	for _, k := range keys {
		b.WriteString(" " + k + "=" + fmt.Sprint(e.Fields[k]))
	}
	f.lines = append(f.lines, b.String())
}

func (f *fakeSourceLog) Remove(_ context.Context, hash string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, hash)
	return nil
}

func (f *fakeSourceLog) Read(context.Context, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.lines, "\n"), nil
}

func (f *fakeSourceLog) contains(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, line := range f.lines {
		if strings.Contains(line, s) {
			return true
		}
	}
	return false
}

func (f *fakeSourceLog) joined() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.lines, "\n")
}

func toolResponse(name, args string) interfaces.AgentResponse {
	return interfaces.AgentResponse{
		Message: interfaces.AgentMessage{Role: "assistant", ToolCalls: []interfaces.AgentToolCall{
			{ID: "call", Name: name, Arguments: args},
		}},
		PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110,
	}
}

// claimsResponse is the distill's structured ANSWER: the claims of one part, as data.
func claimsResponse(claims ...string) interfaces.AgentResponse {
	return finalResponse(`{"claims":[` + strings.Join(claims, ",") + `]}`)
}

// completeResponse is the audit's answer: nothing is missing.
func completeResponse() interfaces.AgentResponse {
	return finalResponse(`{"complete":true,"claims":[]}`)
}

func truncatedAnswer(text string) interfaces.AgentResponse {
	r := finalResponse(text)
	r.FinishReason = interfaces.FinishReasonLength
	return r
}

func finalResponse(text string) interfaces.AgentResponse {
	return interfaces.AgentResponse{
		Message:      interfaces.AgentMessage{Role: "assistant", Content: text},
		PromptTokens: 20, TotalTokens: 20,
	}
}
