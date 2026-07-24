package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type ResponsesRequest struct {
	Model            string           `json:"model"`
	Instructions     string           `json:"instructions,omitempty"`
	SafetyIdentifier string           `json:"safety_identifier,omitempty"`
	Input            []ResponseInput  `json:"input"`
	Stream           bool             `json:"stream"`
	Store            bool             `json:"store"`
	Reasoning        ReasoningOptions `json:"reasoning"`
	Tools            []map[string]any `json:"tools,omitempty"`
	ToolChoice       string           `json:"tool_choice,omitempty"`
}

type ResponseInput struct {
	Role    string          `json:"role,omitempty"`
	Content any             `json:"content,omitempty"`
	Raw     json.RawMessage `json:"-"`
}

type ResponseContent struct {
	Type      string
	Text      string
	ImagePath string
	MediaType string
	ByteSize  int64
}

type ReasoningOptions struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary"`
}

type HTTPError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("provider returned HTTP %d (%s)", e.StatusCode, e.Code)
}

var errRequestTooLarge = errors.New("provider request body too large")

func (c *Client) StartResponse(ctx context.Context, request ResponsesRequest) (*http.Response, error) {
	body, bodyPath, size, err := c.compileRequest(request)
	if err != nil {
		if errors.Is(err, errRequestTooLarge) {
			return nil, fmt.Errorf("provider request body exceeds %d bytes", c.requestMax)
		}
		return nil, fmt.Errorf("encode provider request: %w", err)
	}
	defer func() {
		_ = body.Close()
		_ = os.Remove(bodyPath)
	}()

	endpoint := *c.baseURL
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/responses"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = size
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return nil, decodeHTTPError(resp.StatusCode, raw)
}

func (c *Client) compileRequest(request ResponsesRequest) (*os.File, string, int64, error) {
	tempDir := c.requestTempDir
	if tempDir == "" {
		tempDir = filepath.Join(os.TempDir(), "owui-personal-slim-provider")
	}
	if err := os.MkdirAll(tempDir, 0o700); err != nil {
		return nil, "", 0, err
	}
	file, err := os.CreateTemp(tempDir, "request-*.json")
	if err != nil {
		return nil, "", 0, err
	}
	path := file.Name()
	cleanup := func(compilationErr error) (*os.File, string, int64, error) {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, "", 0, compilationErr
	}
	if err := file.Chmod(0o600); err != nil {
		return cleanup(err)
	}

	writer := &limitedWriter{writer: file, remaining: c.requestMax}
	if err := writeResponsesRequest(writer, request); err != nil {
		return cleanup(err)
	}
	if err := file.Sync(); err != nil {
		return cleanup(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return cleanup(err)
	}
	return file, path, writer.written, nil
}

type limitedWriter struct {
	writer    io.Writer
	remaining int64
	written   int64
}

func (w *limitedWriter) Write(value []byte) (int, error) {
	if int64(len(value)) > w.remaining {
		return 0, errRequestTooLarge
	}
	count, err := w.writer.Write(value)
	w.remaining -= int64(count)
	w.written += int64(count)
	return count, err
}

func writeResponsesRequest(output io.Writer, request ResponsesRequest) error {
	write := func(value string) error {
		_, err := io.WriteString(output, value)
		return err
	}
	writeJSON := func(value any) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_, err = output.Write(raw)
		return err
	}

	if err := write(`{"model":`); err != nil {
		return err
	}
	if err := writeJSON(request.Model); err != nil {
		return err
	}
	if strings.TrimSpace(request.Instructions) != "" {
		if err := write(`,"instructions":`); err != nil {
			return err
		}
		if err := writeJSON(request.Instructions); err != nil {
			return err
		}
	}
	if request.SafetyIdentifier != "" {
		if err := write(`,"safety_identifier":`); err != nil {
			return err
		}
		if err := writeJSON(request.SafetyIdentifier); err != nil {
			return err
		}
	}
	if err := write(`,"input":[`); err != nil {
		return err
	}
	for index, input := range request.Input {
		if index > 0 {
			if err := write(","); err != nil {
				return err
			}
		}
		if len(input.Raw) > 0 {
			if !json.Valid(input.Raw) {
				return errors.New("invalid raw provider input item")
			}
			if _, err := output.Write(input.Raw); err != nil {
				return err
			}
			continue
		}
		if err := write(`{"role":`); err != nil {
			return err
		}
		if err := writeJSON(input.Role); err != nil {
			return err
		}
		if err := write(`,"content":`); err != nil {
			return err
		}
		contents, typed := input.Content.([]ResponseContent)
		if !typed {
			if err := writeJSON(input.Content); err != nil {
				return err
			}
		} else {
			if err := write("["); err != nil {
				return err
			}
			for contentIndex, content := range contents {
				if contentIndex > 0 {
					if err := write(","); err != nil {
						return err
					}
				}
				if content.ImagePath == "" {
					if err := writeJSON(map[string]string{"type": content.Type, "text": content.Text}); err != nil {
						return err
					}
					continue
				}
				if err := write(`{"type":"input_image","image_url":"data:`); err != nil {
					return err
				}
				if err := write(content.MediaType); err != nil {
					return err
				}
				if err := write(`;base64,`); err != nil {
					return err
				}
				image, err := os.Open(content.ImagePath)
				if err != nil {
					return err
				}
				encoder := base64.NewEncoder(base64.StdEncoding, output)
				copied, copyErr := io.Copy(encoder, io.LimitReader(image, content.ByteSize+1))
				closeErr := encoder.Close()
				imageCloseErr := image.Close()
				if copyErr != nil {
					return copyErr
				}
				if closeErr != nil {
					return closeErr
				}
				if imageCloseErr != nil {
					return imageCloseErr
				}
				if copied != content.ByteSize {
					return errors.New("attachment size does not match database")
				}
				if err := write(`"}`); err != nil {
					return err
				}
			}
			if err := write("]"); err != nil {
				return err
			}
		}
		if err := write("}"); err != nil {
			return err
		}
	}
	if err := write(`],"stream":`); err != nil {
		return err
	}
	if err := writeJSON(request.Stream); err != nil {
		return err
	}
	if err := write(`,"store":`); err != nil {
		return err
	}
	if err := writeJSON(request.Store); err != nil {
		return err
	}
	if err := write(`,"reasoning":`); err != nil {
		return err
	}
	if err := writeJSON(request.Reasoning); err != nil {
		return err
	}
	if len(request.Tools) > 0 {
		if err := write(`,"tools":`); err != nil {
			return err
		}
		if err := writeJSON(request.Tools); err != nil {
			return err
		}
	}
	if request.ToolChoice != "" {
		if err := write(`,"tool_choice":`); err != nil {
			return err
		}
		if err := writeJSON(request.ToolChoice); err != nil {
			return err
		}
	}
	return write("}")
}

type TextResult struct {
	ResponseID   string
	Text         string
	InputTokens  int64
	OutputTokens int64
}

func (c *Client) CompleteText(ctx context.Context, request ResponsesRequest) (TextResult, error) {
	request.Stream = false
	request.Store = false
	request.Tools = nil
	request.ToolChoice = ""
	response, err := c.StartResponse(ctx, request)
	if err != nil {
		return TextResult{}, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return TextResult{}, fmt.Errorf("read provider text response: %w", err)
	}
	var payload struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return TextResult{}, fmt.Errorf("%w: decode text response", ErrBadResponse)
	}
	if payload.Status == "failed" || payload.Error.Code != "" {
		return TextResult{}, &HTTPError{StatusCode: http.StatusBadGateway, Code: payload.Error.Code, Message: payload.Error.Message}
	}
	var text strings.Builder
	for _, item := range payload.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "output_text" {
				text.WriteString(content.Text)
			}
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return TextResult{}, fmt.Errorf("%w: empty text response", ErrBadResponse)
	}
	return TextResult{
		ResponseID: payload.ID, Text: text.String(),
		InputTokens: payload.Usage.InputTokens, OutputTokens: payload.Usage.OutputTokens,
	}, nil
}
