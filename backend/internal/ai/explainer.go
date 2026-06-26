// Package ai is KubePilot's AI explanation layer. It is strictly post-analysis:
// it takes deterministic findings already produced by the rule engine and asks
// Claude to explain and prioritize them. It never generates findings — the model
// only ever sees, and is instructed to only ever discuss, the findings handed to
// it. If something isn't in the deterministic output, the AI does not invent it.
package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Sentinel errors for callers to branch on.
var (
	ErrDisabled   = errors.New("ai explanation is not configured")
	ErrNoFindings = errors.New("no findings to explain")
)

const defaultModel = "claude-opus-4-8"

// Config holds the Anthropic API settings. APIKey empty means the layer is
// disabled and the API degrades to 503 on explain endpoints.
type Config struct {
	APIKey    string
	Model     string
	MaxTokens int64
	// BaseURL overrides the Anthropic API endpoint. Used by tests to point the
	// SDK at an httptest server; empty means the real API.
	BaseURL string
}

// Finding is the analyzer-agnostic shape every report is reduced to before being
// handed to the model. It carries only what the model needs to explain a finding
// — not the freedom to introduce new ones.
type Finding struct {
	Analyzer string `json:"analyzer"`
	Type     string `json:"type"`
	Severity string `json:"severity"`
	Resource string `json:"resource"`
	Message  string `json:"message"`
}

// ExplainRequest bundles the deterministic findings plus context for the model.
type ExplainRequest struct {
	ClusterID string
	Analyzer  string
	Context   string // e.g. "health score 30/100" — surrounding deterministic facts
	Findings  []Finding
}

// ExplainResponse is the model's explanation over the provided findings.
type ExplainResponse struct {
	Model             string `json:"model"`
	Explanation       string `json:"explanation"`
	FindingsExplained int    `json:"findingsExplained"`
}

// Explainer wraps the Anthropic client. The zero value (no API key) is disabled.
type Explainer struct {
	cfg     Config
	client  anthropic.Client
	enabled bool
}

// NewExplainer constructs an Explainer. With no API key it returns a disabled
// instance whose Explain always returns ErrDisabled — the server boots fine
// without AI configured.
func NewExplainer(cfg Config) *Explainer {
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 1024
	}
	e := &Explainer{cfg: cfg}
	if cfg.APIKey == "" {
		return e
	}
	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	e.client = anthropic.NewClient(opts...)
	e.enabled = true
	return e
}

// Enabled reports whether an API key was configured.
func (e *Explainer) Enabled() bool { return e.enabled }

// Model returns the configured model id.
func (e *Explainer) Model() string { return e.cfg.Model }

// Explain asks Claude to explain the provided deterministic findings. It returns
// ErrDisabled when no key is configured and ErrNoFindings when the slice is
// empty (there is nothing to explain — and nothing the model is allowed to
// invent in its place).
func (e *Explainer) Explain(ctx context.Context, req ExplainRequest) (ExplainResponse, error) {
	if !e.enabled {
		return ExplainResponse{}, ErrDisabled
	}
	if len(req.Findings) == 0 {
		return ExplainResponse{}, ErrNoFindings
	}

	msg, err := e.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(e.cfg.Model),
		MaxTokens: e.cfg.MaxTokens,
		System:    []anthropic.TextBlockParam{{Text: systemPrompt}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(buildUserContent(req))),
		},
	})
	if err != nil {
		return ExplainResponse{}, fmt.Errorf("anthropic messages: %w", err)
	}

	return ExplainResponse{
		Model:             string(msg.Model),
		Explanation:       extractText(msg),
		FindingsExplained: len(req.Findings),
	}, nil
}

// systemPrompt is the grounding contract: explain only what you are given.
const systemPrompt = `You are an SRE assistant embedded in KubePilot, a Kubernetes reliability platform.

KubePilot's rule engine has ALREADY analyzed a cluster deterministically and produced the findings below. Your job is to EXPLAIN and PRIORITIZE those findings for an on-call engineer — not to perform your own analysis.

Strict rules:
- Only discuss the findings provided. Do NOT invent, infer, or speculate about issues that are not in the list.
- Do NOT claim the cluster has problems that aren't represented in the findings, and do NOT declare it healthy beyond what the findings show.
- Reference findings by their exact type and resource.
- If asked implicitly about something not present, state that it is not part of this analysis.

Output format (concise markdown):
1. A one or two sentence summary of the overall situation, grounded in the findings.
2. Prioritized remediation, most severe first, grouped by severity. For each, give the concrete kubectl/YAML-level action an engineer would take.
Keep it tight and operational — this is read during an incident.`

// buildUserContent renders the deterministic findings into the user turn.
func buildUserContent(req ExplainRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Cluster: %s\n", req.ClusterID)
	fmt.Fprintf(&b, "Analyzer: %s\n", req.Analyzer)
	if req.Context != "" {
		fmt.Fprintf(&b, "Context: %s\n", req.Context)
	}
	fmt.Fprintf(&b, "\nDeterministic findings (%d):\n", len(req.Findings))
	for i, f := range req.Findings {
		resource := f.Resource
		if resource == "" {
			resource = "(cluster)"
		}
		fmt.Fprintf(&b, "%d. [%s] %s — %s: %s\n", i+1, f.Severity, f.Type, resource, f.Message)
	}
	b.WriteString("\nExplain and prioritize ONLY these findings.")
	return b.String()
}

func extractText(m *anthropic.Message) string {
	var b strings.Builder
	for _, block := range m.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return strings.TrimSpace(b.String())
}
