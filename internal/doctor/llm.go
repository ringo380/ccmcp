package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Provider identifies which LLM API to use for file review.
type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOpenAI    Provider = "openai"
	ProviderClaudeCLI Provider = "claude-cli"
)

// ErrClaudeCLINotFound is returned when the local `claude` binary cannot be
// resolved on $PATH. Callers can use errors.Is to drive a friendly UI hint.
var ErrClaudeCLINotFound = errors.New("claude CLI not found in PATH")

// APIError is returned for non-2xx HTTP responses from Anthropic/OpenAI. It
// carries a parsed message (when the body matched the standard
// {"error":{"message":"..."}} shape) plus the raw body for diagnostics.
type APIError struct {
	Provider string
	Status   int
	Message  string
	Raw      string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("%s API %d: %s", e.Provider, e.Status, e.Message)
	}
	if e.Raw != "" {
		return fmt.Sprintf("%s API %d: %s", e.Provider, e.Status, e.Raw)
	}
	return fmt.Sprintf("%s API %d", e.Provider, e.Status)
}

// parseAPIError attempts to extract an `error.message` string from a JSON
// response body shared by Anthropic and OpenAI. Returns "" on any parse miss.
func parseAPIError(raw []byte) string {
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return ""
	}
	return env.Error.Message
}

// ReviewOptions configures the LLM review call.
type ReviewOptions struct {
	Provider Provider
	APIKey   string // if empty, reads from env
	Model    string // if empty, uses provider default
}

func (o *ReviewOptions) apiKey() string {
	if o.APIKey != "" {
		return o.APIKey
	}
	switch o.Provider {
	case ProviderClaudeCLI:
		return ""
	case ProviderOpenAI:
		return os.Getenv("OPENAI_API_KEY")
	default:
		return os.Getenv("ANTHROPIC_API_KEY")
	}
}

func (o *ReviewOptions) model() string {
	if o.Model != "" {
		return o.Model
	}
	switch o.Provider {
	case ProviderClaudeCLI:
		return ""
	case ProviderOpenAI:
		return "gpt-4o"
	default:
		return DefaultAnthropicModel
	}
}

// DefaultAnthropicModel is the cheap-but-capable model used for doctor review
// and asset-lint fixes. Haiku is plenty for the mechanical edits these tasks
// describe; Sonnet/Opus burned tokens on prose responses that often didn't
// even invoke the Edit tool. Callers can override via ReviewOptions.Model or
// the `--model` flag.
const DefaultAnthropicModel = "claude-haiku-4-5"

// resolveProvider picks the LLM backend. Explicit caller opt-ins win first: a
// set Provider, then an explicit APIKey (which forces the HTTP API). Absent
// those, we prefer the local `claude` CLI when it's on $PATH — headless
// `claude --print` over the user's OAuth subscription is the intended default,
// not an env API key. Env keys (ANTHROPIC_API_KEY, then OPENAI_API_KEY) are a
// fallback for when the CLI isn't installed; failing everything, default to
// anthropic so the caller gets a friendly key-missing error.
func (o *ReviewOptions) resolveProvider() Provider {
	if o.Provider != "" {
		return o.Provider
	}
	if o.APIKey != "" {
		return ProviderAnthropic
	}
	if _, err := exec.LookPath("claude"); err == nil {
		return ProviderClaudeCLI
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return ProviderAnthropic
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		return ProviderOpenAI
	}
	return ProviderAnthropic
}

// Review sends the content of path to the configured LLM and returns its feedback.
func Review(path string, opts ReviewOptions) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	provider := opts.resolveProvider()
	opts.Provider = provider

	prompt := buildPrompt(path, string(content))

	switch provider {
	case ProviderClaudeCLI:
		return callClaudeCLI(prompt, opts.model())
	case ProviderOpenAI:
		apiKey := opts.apiKey()
		if apiKey == "" {
			return "", fmt.Errorf("no API key: set OPENAI_API_KEY or pass --api-key")
		}
		return callOpenAI(prompt, opts.model(), apiKey)
	default:
		apiKey := opts.apiKey()
		if apiKey == "" {
			return "", fmt.Errorf("no API key: set ANTHROPIC_API_KEY or pass --api-key (or install the claude CLI for offline review)")
		}
		return callAnthropic(prompt, opts.model(), apiKey)
	}
}

// BundleEntry describes one file to include in a bundled review.
type BundleEntry struct {
	Path    string
	Content string
}

// ReviewBundle sends one LLM call covering every entry in `files`, returning a
// single combined response. Per-file iteration in Review() was costly: with
// Sonnet/Opus defaults a multi-file project paid for N independent prompts
// when one bundled call would have produced equivalent guidance. Errors from a
// single missing file are non-fatal — the call proceeds with the remaining
// entries and the response notes the omission.
func ReviewBundle(files []BundleEntry, opts ReviewOptions) (string, error) {
	if len(files) == 0 {
		return "", fmt.Errorf("ReviewBundle: no files to review")
	}
	provider := opts.resolveProvider()
	opts.Provider = provider
	prompt := buildBundlePrompt(files)

	switch provider {
	case ProviderClaudeCLI:
		return callClaudeCLI(prompt, opts.model())
	case ProviderOpenAI:
		apiKey := opts.apiKey()
		if apiKey == "" {
			return "", fmt.Errorf("no API key: set OPENAI_API_KEY or pass --api-key")
		}
		return callOpenAI(prompt, opts.model(), apiKey)
	default:
		apiKey := opts.apiKey()
		if apiKey == "" {
			return "", fmt.Errorf("no API key: set ANTHROPIC_API_KEY or pass --api-key (or install the claude CLI for offline review)")
		}
		return callAnthropic(prompt, opts.model(), apiKey)
	}
}

func buildBundlePrompt(files []BundleEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are reviewing %d documentation file(s) used by Claude Code, Anthropic's AI coding assistant. Produce a single combined review covering all of them.\n\n", len(files))
	b.WriteString(`For each file, evaluate:
1. Clarity — unambiguous and actionable instructions?
2. Completeness — obvious gaps for a development project?
3. Redundancy — duplicated content, or content that belongs in code/git?
4. Formatting — structure easy to scan?
5. Staleness — outdated tool versions, deprecated workflows?

Respond with one section per file. Use bullet points; skip categories with no issues. End each section with a 1-sentence verdict. After the last file, give a 1-sentence cross-file summary.

`)
	for i, f := range files {
		fmt.Fprintf(&b, "── File %d/%d: %s ──\n<file path=%q>\n%s\n</file>\n\n", i+1, len(files), f.Path, f.Path, f.Content)
	}
	return b.String()
}

func buildPrompt(path, content string) string {
	base := strings.ToLower(strings.TrimSuffix(strings.ToLower(fmt.Sprintf("%s", path)), ".md"))
	fileType := "configuration"
	if strings.HasSuffix(base, "memory") {
		fileType = "memory index"
	} else if strings.HasSuffix(base, "claude") {
		fileType = "CLAUDE.md project instructions"
	}

	return fmt.Sprintf(`You are reviewing a %s file used by Claude Code, Anthropic's AI coding assistant.

Analyse the following file for:
1. Clarity — are the instructions unambiguous and actionable?
2. Completeness — are there obvious gaps for a development project?
3. Redundancy — any information duplicated or that belongs in code/git instead?
4. Formatting — is the structure easy to scan quickly?
5. Staleness — any clearly outdated content (old tool versions, deprecated workflows)?

File path: %s

<file>
%s
</file>

Provide concise, actionable feedback grouped by the five categories above. Use bullet points. Skip categories with no issues. End with a 1-sentence overall verdict.`, fileType, path, content)
}

// callAnthropic sends a request to the Anthropic Messages API.
func callAnthropic(prompt, model, apiKey string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 1024,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
	})

	req, err := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("content-type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", buildAPIError("anthropic", resp.StatusCode, raw)
	}

	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(res.Content) == 0 {
		return "", fmt.Errorf("empty response from Anthropic")
	}
	return res.Content[0].Text, nil
}

// callOpenAI sends a request to the OpenAI Chat Completions API.
func callOpenAI(prompt, model, apiKey string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model": model,
		"messages": []map[string]any{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 1024,
	})

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai request: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", buildAPIError("openai", resp.StatusCode, raw)
	}

	var res struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if len(res.Choices) == 0 {
		return "", fmt.Errorf("empty response from OpenAI")
	}
	return res.Choices[0].Message.Content, nil
}

// buildAPIError parses the response body, applies the 401 hint, and returns a
// typed *APIError suitable for callers to errors.As against.
func buildAPIError(provider string, status int, raw []byte) *APIError {
	msg := parseAPIError(raw)
	if status == 401 {
		hint := "key rejected — run /login or use --provider claude-cli"
		if msg == "" {
			msg = hint
		} else {
			msg = msg + " (" + hint + ")"
		}
	}
	return &APIError{
		Provider: provider,
		Status:   status,
		Message:  msg,
		Raw:      strings.TrimSpace(string(raw)),
	}
}

// callClaudeCLI shells out to the local `claude` binary using --print, piping
// the prompt over stdin and capturing stdout. stderr is captured separately
// and surfaced only on non-zero exit. No API key required.
func callClaudeCLI(prompt, model string) (string, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return "", ErrClaudeCLINotFound
	}

	args := []string{"--print"}
	if model != "" {
		args = append(args, "--model", model)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("claude CLI timed out after 120s")
		}
		stderrTrimmed := strings.TrimSpace(stderr.String())
		if exitErr, ok := err.(*exec.ExitError); ok {
			if stderrTrimmed != "" {
				return "", fmt.Errorf("claude CLI exit %d: %s", exitErr.ExitCode(), stderrTrimmed)
			}
			return "", fmt.Errorf("claude CLI exit %d", exitErr.ExitCode())
		}
		if stderrTrimmed != "" {
			return "", fmt.Errorf("claude CLI: %s: %w", stderrTrimmed, err)
		}
		return "", fmt.Errorf("claude CLI: %w", err)
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return "", fmt.Errorf("empty response from claude CLI")
	}
	return out, nil
}

func envName(p Provider) string {
	switch p {
	case ProviderOpenAI:
		return "OPENAI_API_KEY"
	default:
		return "ANTHROPIC_API_KEY"
	}
}
