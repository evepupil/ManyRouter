package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/evepupil/ManyRouter/internal/domain/value"
)

const maxProbeResponseBytes int64 = 64 << 10

// ProbeRequest describes one OpenAI-compatible chat completion probe.
type ProbeRequest struct {
	Model       string
	Prompt      string
	Temperature float64
	TopP        float64
	MaxTokens   int
	Stream      bool
}

// ProbeResult contains the observable response facts needed by evaluation.
type ProbeResult struct {
	Text             string
	ResponseModel    string
	HTTPStatus       int
	FinishReason     string
	InputTokens      int64
	OutputTokens     int64
	FirstTokenMillis *int64
	TotalMillis      int64
	StreamCompleted  bool
}

// ProbeClient executes bounded OpenAI-compatible probe requests.
type ProbeClient struct {
	client *http.Client
}

func NewProbeClient(client *http.Client) (*ProbeClient, error) {
	if client == nil {
		return nil, errors.New("supplier probe HTTP client is required")
	}
	clientCopy := *client
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &ProbeClient{client: &clientCopy}, nil
}

// Probe sends one request to the supplier. HTTP failures are returned as
// business results; transport and malformed protocol responses return errors.
func (c *ProbeClient) Probe(ctx context.Context, baseURL string, key []byte, input ProbeRequest) (result ProbeResult, err error) {
	endpoint, err := value.NormalizeOpenAICompatibleBaseURL(baseURL)
	if err != nil {
		return ProbeResult{}, errors.New("supplier probe base URL is invalid")
	}
	if len(key) == 0 {
		return ProbeResult{}, errors.New("supplier probe key is required")
	}

	payload, err := json.Marshal(chatCompletionRequest{
		Model:       input.Model,
		Messages:    []chatCompletionMessage{{Role: "user", Content: input.Prompt}},
		Temperature: input.Temperature,
		TopP:        input.TopP,
		MaxTokens:   input.MaxTokens,
		Stream:      input.Stream,
	})
	if err != nil {
		return ProbeResult{}, errors.New("encode supplier probe request")
	}
	defer clear(payload)

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return ProbeResult{}, errors.New("create supplier probe request")
	}
	request.Header.Set("Accept", "application/json")
	if input.Stream {
		request.Header.Set("Accept", "text/event-stream")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+string(key))

	startedAt := time.Now()
	defer func() {
		result.TotalMillis = time.Since(startedAt).Milliseconds()
	}()
	response, err := c.client.Do(request)
	if err != nil {
		return ProbeResult{}, errors.New("supplier probe request failed")
	}
	defer func() { _ = response.Body.Close() }()
	result.HTTPStatus = response.StatusCode
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return result, nil
	}

	if input.Stream {
		return readStreamProbe(response.Body, startedAt, result)
	}
	return readJSONProbe(response.Body, startedAt, result)
}

type chatCompletionRequest struct {
	Model       string                  `json:"model"`
	Messages    []chatCompletionMessage `json:"messages"`
	Temperature float64                 `json:"temperature"`
	TopP        float64                 `json:"top_p"`
	MaxTokens   int                     `json:"max_tokens"`
	Stream      bool                    `json:"stream"`
}

type chatCompletionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionResponse struct {
	Model   string                 `json:"model"`
	Choices []chatCompletionChoice `json:"choices"`
	Usage   *chatCompletionUsage   `json:"usage"`
}

type chatCompletionChoice struct {
	Message      chatCompletionMessage `json:"message"`
	Delta        chatCompletionMessage `json:"delta"`
	FinishReason *string               `json:"finish_reason"`
}

type chatCompletionUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

func readJSONProbe(body io.Reader, startedAt time.Time, result ProbeResult) (ProbeResult, error) {
	payload, err := readProbePayload(body)
	if err != nil {
		return result, err
	}
	var response chatCompletionResponse
	if err := json.Unmarshal(payload, &response); err != nil || len(response.Choices) == 0 {
		return result, errors.New("supplier returned an invalid chat completion response")
	}
	choice := response.Choices[0]
	result.Text = choice.Message.Content
	result.ResponseModel = response.Model
	result.FinishReason = stringValue(choice.FinishReason)
	if response.Usage != nil {
		result.InputTokens = response.Usage.PromptTokens
		result.OutputTokens = response.Usage.CompletionTokens
	}
	if strings.TrimSpace(result.Text) != "" {
		firstTokenMillis := time.Since(startedAt).Milliseconds()
		result.FirstTokenMillis = &firstTokenMillis
	}
	return result, nil
}

func readProbePayload(body io.Reader) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(body, maxProbeResponseBytes+1))
	if err != nil {
		return nil, errors.New("read supplier probe response")
	}
	if int64(len(payload)) > maxProbeResponseBytes {
		return nil, errors.New("supplier probe response exceeded 64 KiB")
	}
	return payload, nil
}

func readStreamProbe(body io.Reader, startedAt time.Time, result ProbeResult) (ProbeResult, error) {
	limited := &io.LimitedReader{R: body, N: maxProbeResponseBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), int(maxProbeResponseBytes+1))
	eventData := make([]string, 0, 1)
	sawData := false

	for scanner.Scan() {
		if maxProbeResponseBytes+1-limited.N > maxProbeResponseBytes {
			return result, errors.New("supplier probe response exceeded 64 KiB")
		}
		line := scanner.Text()
		if line == "" {
			done, err := applyProbeSSEEvent(eventData, startedAt, &result)
			if err != nil {
				return result, err
			}
			eventData = eventData[:0]
			if done {
				return result, nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			sawData = true
			value := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			eventData = append(eventData, value)
		}
	}
	if maxProbeResponseBytes+1-limited.N > maxProbeResponseBytes {
		return result, errors.New("supplier probe response exceeded 64 KiB")
	}
	if err := scanner.Err(); err != nil {
		return result, errors.New("read supplier probe stream")
	}
	if !sawData {
		return result, errors.New("supplier returned an invalid chat completion stream")
	}
	if len(eventData) > 0 {
		if _, err := applyProbeSSEEvent(eventData, startedAt, &result); err != nil {
			return result, err
		}
	}
	return result, nil
}

func applyProbeSSEEvent(lines []string, startedAt time.Time, result *ProbeResult) (bool, error) {
	if len(lines) == 0 {
		return false, nil
	}
	data := strings.Join(lines, "\n")
	if data == "[DONE]" {
		result.StreamCompleted = true
		return true, nil
	}
	var chunk chatCompletionResponse
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return false, errors.New("supplier returned an invalid chat completion stream")
	}
	if chunk.Model != "" {
		result.ResponseModel = chunk.Model
	}
	if chunk.Usage != nil {
		result.InputTokens = chunk.Usage.PromptTokens
		result.OutputTokens = chunk.Usage.CompletionTokens
	}
	if len(chunk.Choices) == 0 {
		return false, nil
	}
	choice := chunk.Choices[0]
	content := choice.Delta.Content
	result.Text += content
	if result.FirstTokenMillis == nil && strings.TrimSpace(content) != "" {
		firstTokenMillis := time.Since(startedAt).Milliseconds()
		result.FirstTokenMillis = &firstTokenMillis
	}
	if choice.FinishReason != nil {
		result.FinishReason = *choice.FinishReason
	}
	return false, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
