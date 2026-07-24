package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	body, err := json.Marshal(map[string]any{
		"model":           request.Model,
		"prompt":          request.Prompt,
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
	raw, readErr := io.ReadAll(io.LimitReader(response.Body, c.requestMax+1))
	if readErr != nil {
		return ImageGenerationResult{}, fmt.Errorf("%w: read image response", ErrBadResponse)
	}
	if int64(len(raw)) > c.requestMax {
		return ImageGenerationResult{}, fmt.Errorf("%w: image response exceeds %d bytes", ErrBadResponse, c.requestMax)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ImageGenerationResult{}, decodeHTTPError(response.StatusCode, raw)
	}

	var payload struct {
		Created int64 `json:"created"`
		Data    []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
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

func decodeHTTPError(statusCode int, raw []byte) error {
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &payload)
	code := strings.TrimSpace(payload.Error.Code)
	if code == "" {
		code = strings.TrimSpace(payload.Error.Type)
	}
	if code == "" {
		code = "provider_http_error"
	}
	message := strings.TrimSpace(payload.Error.Message)
	if message == "" {
		message = http.StatusText(statusCode)
	}
	return &HTTPError{StatusCode: statusCode, Code: code, Message: message}
}
