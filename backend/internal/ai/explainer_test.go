package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sampleFindings() []Finding {
	return []Finding{
		{Analyzer: "workload", Type: "CrashLoopBackOff", Severity: "critical", Resource: "prod/api-0/api", Message: "container is in CrashLoopBackOff"},
		{Analyzer: "workload", Type: "OOMKilled", Severity: "critical", Resource: "prod/api-0/api", Message: "container was OOMKilled"},
	}
}

func TestExplainer_DisabledWithoutKey(t *testing.T) {
	e := NewExplainer(Config{})
	if e.Enabled() {
		t.Fatal("explainer should be disabled with no API key")
	}
	_, err := e.Explain(context.Background(), ExplainRequest{Findings: sampleFindings()})
	if err != ErrDisabled {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
}

func TestExplainer_NoFindings(t *testing.T) {
	e := NewExplainer(Config{APIKey: "test-key"})
	_, err := e.Explain(context.Background(), ExplainRequest{ClusterID: "c", Analyzer: "workload"})
	if err != ErrNoFindings {
		t.Fatalf("expected ErrNoFindings, got %v", err)
	}
}

func TestExplainer_Defaults(t *testing.T) {
	e := NewExplainer(Config{APIKey: "k"})
	if e.Model() != defaultModel {
		t.Fatalf("expected default model %q, got %q", defaultModel, e.Model())
	}
	if e.cfg.MaxTokens != 1024 {
		t.Fatalf("expected default max tokens 1024, got %d", e.cfg.MaxTokens)
	}
}

func TestExplainer_ExplainOverFindings(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "msg_1",
			"type": "message",
			"role": "assistant",
			"model": "claude-opus-4-8",
			"content": [{"type": "text", "text": "Two critical container failures in prod/api-0. Fix OOM first: raise the memory limit."}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 100, "output_tokens": 20}
		}`))
	}))
	defer srv.Close()

	e := NewExplainer(Config{APIKey: "test-key", Model: "claude-opus-4-8", MaxTokens: 512, BaseURL: srv.URL})
	if !e.Enabled() {
		t.Fatal("explainer should be enabled with an API key")
	}

	resp, err := e.Explain(context.Background(), ExplainRequest{
		ClusterID: "kind-dev",
		Analyzer:  "workload",
		Context:   "5 pods, 2 findings",
		Findings:  sampleFindings(),
	})
	if err != nil {
		t.Fatalf("Explain returned error: %v", err)
	}
	if !strings.Contains(resp.Explanation, "OOM") {
		t.Fatalf("unexpected explanation: %q", resp.Explanation)
	}
	if resp.Model != "claude-opus-4-8" {
		t.Fatalf("expected model echoed back, got %q", resp.Model)
	}
	if resp.FindingsExplained != 2 {
		t.Fatalf("expected 2 findings explained, got %d", resp.FindingsExplained)
	}

	// The request body must carry the deterministic findings to the model — this
	// is what makes the AI an explanation layer rather than a freestanding one.
	raw, _ := json.Marshal(gotBody)
	if !strings.Contains(string(raw), "CrashLoopBackOff") || !strings.Contains(string(raw), "OOMKilled") {
		t.Fatalf("request body did not include the deterministic findings: %s", raw)
	}
}

func TestBuildUserContent_GroundsOnFindings(t *testing.T) {
	content := buildUserContent(ExplainRequest{
		ClusterID: "kind-dev",
		Analyzer:  "security",
		Findings: []Finding{
			{Type: "PrivilegedContainer", Severity: "critical", Resource: "prod/web/web", Message: "runs privileged"},
		},
	})
	for _, want := range []string{"kind-dev", "security", "PrivilegedContainer", "prod/web/web", "ONLY these findings"} {
		if !strings.Contains(content, want) {
			t.Errorf("user content missing %q:\n%s", want, content)
		}
	}
}

func TestSystemPrompt_ForbidsInvention(t *testing.T) {
	for _, want := range []string{"Only discuss the findings provided", "Do NOT invent"} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("system prompt missing grounding instruction %q", want)
		}
	}
}
