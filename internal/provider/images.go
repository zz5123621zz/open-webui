package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"
)

type ImageGenerationRequest struct {
	Model  string
	Prompt string
}

type ImageGenerationResult struct {
	ResponseID string
	Base64     string
}

func (c *Client) GenerateImage(ctx context.Context, request ImageGenerationRequest) (ImageGenerationResult, error) {
	prompt := compactImagePrompt(request.Prompt)
	promptBytes := len(prompt)
	if c.imagePromptMax > 0 && promptBytes > c.imagePromptMax {
		return ImageGenerationResult{}, fmt.Errorf(
			"%w: prompt is %d bytes after whitespace compaction (maximum %d)",
			ErrImagePromptTooLong, promptBytes, c.imagePromptMax,
		)
	}
	body, err := json.Marshal(map[string]any{
		"model":           request.Model,
		"prompt":          prompt,
		"n":               1,
		"response_format": "b64_json",
	})
	if err != nil {
		return ImageGenerationResult{}, fmt.Errorf("encode image request: %w", err)
	}
	if int64(len(body)) > c.requestMax {
		return ImageGenerationResult{}, fmt.Errorf("provider request body exceeds %d bytes", c.requestMax)
	}

	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/images/generations"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return ImageGenerationResult{}, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")

	response, err := c.streamClient.Do(httpRequest)
	if err != nil {
		return ImageGenerationResult{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// Provider error bodies are small; read a bounded amount for diagnostics.
		errBody, _ := io.ReadAll(io.LimitReader(response.Body, 8<<10))
		return ImageGenerationResult{}, decodeHTTPError(response.StatusCode, errBody)
	}

	// Stream-decode the success envelope instead of buffering the entire
	// (up to tens of MiB base64) response in memory first. The LimitReader
	// caps how much the decoder can pull, so an oversized response fails to
	// parse rather than ballooning past the container memory budget.
	var payload struct {
		Created int64 `json:"created"`
		Data    []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, c.requestMax+1))
	if err := decoder.Decode(&payload); err != nil {
		return ImageGenerationResult{}, fmt.Errorf("%w: decode image response", ErrBadResponse)
	}
	if len(payload.Data) != 1 || strings.TrimSpace(payload.Data[0].B64JSON) == "" {
		return ImageGenerationResult{}, fmt.Errorf("%w: image response did not contain one Base64 image", ErrBadResponse)
	}
	return ImageGenerationResult{
		ResponseID: fmt.Sprintf("image-%d", payload.Created),
		Base64:     payload.Data[0].B64JSON,
	}, nil
}

// compactImagePrompt removes layout-only whitespace while preserving spaces
// between ASCII words. CPA's Imagine endpoint measures its 8000-byte prompt
// limit after UTF-8 encoding, so Chinese prompts can exceed the boundary long
// before the application's normal message limit.
func compactImagePrompt(prompt string) string {
	var builder strings.Builder
	builder.Grow(len(prompt))
	var previous rune
	pendingSpace := false
	for _, current := range strings.TrimSpace(prompt) {
		if unicode.IsSpace(current) {
			pendingSpace = builder.Len() > 0
			continue
		}
		if pendingSpace && asciiWordRune(previous) && asciiWordRune(current) {
			builder.WriteByte(' ')
		}
		current = compactPromptPunctuation(current)
		builder.WriteRune(current)
		previous = current
		pendingSpace = false
	}
	return builder.String()
}

func compactPromptPunctuation(value rune) rune {
	switch value {
	case '，', '、':
		return ','
	case '。':
		return '.'
	case '：':
		return ':'
	case '；':
		return ';'
	case '！':
		return '!'
	case '？':
		return '?'
	case '（':
		return '('
	case '）':
		return ')'
	case '【':
		return '['
	case '】':
		return ']'
	case '“', '”':
		return '"'
	case '‘', '’':
		return '\''
	default:
		return value
	}
}

func asciiWordRune(value rune) bool {
	return value <= unicode.MaxASCII && (unicode.IsLetter(value) || unicode.IsDigit(value))
}

func decodeHTTPError(statusCode int, raw []byte) error {
	var payload struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	_ = json.Unmarshal(raw, &payload)

	var nested struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Type    string `json:"type"`
	}
	var flatError string
	_ = json.Unmarshal(payload.Error, &nested)
	_ = json.Unmarshal(payload.Error, &flatError)

	code := strings.TrimSpace(nested.Code)
	if code == "" {
		code = strings.TrimSpace(nested.Type)
	}
	if code == "" {
		code = strings.TrimSpace(payload.Code)
	}
	if code == "" {
		code = "provider_http_error"
	}
	message := strings.TrimSpace(nested.Message)
	if message == "" {
		message = strings.TrimSpace(flatError)
	}
	if message == "" {
		message = strings.TrimSpace(payload.Message)
	}
	if message == "" {
		message = http.StatusText(statusCode)
	}
	return &HTTPError{StatusCode: statusCode, Code: code, Message: message}
}
