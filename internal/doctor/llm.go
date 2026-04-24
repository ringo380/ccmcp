package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Provider identifies which LLM API to use for file review.
type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOpenAI    Provider = "openai"
)

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
	case ProviderOpenAI:
		return "gpt-4o"
	default:
		return "claude-sonnet-4-6"
	}
}

// Review sends the content of path to the configured LLM and returns its feedback.
func Review(path string, opts ReviewOptions) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}

	apiKey := opts.apiKey()
	if apiKey == "" {
		return "", fmt.Errorf("no API key: set %s or pass --api-key", envName(opts.Provider))
	}

	prompt := buildPrompt(path, string(content))

	switch opts.Provider {
	case ProviderOpenAI:
		return callOpenAI(prompt, opts.model(), apiKey)
	default:
		return callAnthropic(prompt, opts.model(), apiKey)
	}
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
		return "", fmt.Errorf("anthropic API %d: %s", resp.StatusCode, string(raw))
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
		return "", fmt.Errorf("openai API %d: %s", resp.StatusCode, string(raw))
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

func envName(p Provider) string {
	switch p {
	case ProviderOpenAI:
		return "OPENAI_API_KEY"
	default:
		return "ANTHROPIC_API_KEY"
	}
}
