package speech

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type aliyunTokenSource struct {
	endpoint        *url.URL
	accessKeyID     string
	accessKeySecret string
	client          *http.Client
	now             func() time.Time
	nonce           func() (string, error)

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func (s *aliyunTokenSource) Token(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.token != "" && now.Add(10*time.Minute).Before(s.expiresAt) {
		return s.token, nil
	}
	nonce, err := s.nonce()
	if err != nil {
		return "", err
	}
	parameters := map[string]string{
		"AccessKeyId":      s.accessKeyID,
		"Action":           "CreateToken",
		"Format":           "JSON",
		"RegionId":         "cn-shanghai",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   nonce,
		"SignatureVersion": "1.0",
		"Timestamp":        now.UTC().Format("2006-01-02T15:04:05Z"),
		"Version":          "2019-02-28",
	}
	parameters["Signature"] = aliyunPOPSignature("GET", parameters, s.accessKeySecret)
	requestURL := *s.endpoint
	query := requestURL.Query()
	for key, value := range parameters {
		query.Set(key, value)
	}
	requestURL.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "application/json")
	response, err := s.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("request Aliyun speech token: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read Aliyun speech token: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("Aliyun speech token returned HTTP %d", response.StatusCode)
	}
	var result struct {
		ErrMsg string `json:"ErrMsg"`
		Token  struct {
			ID         string `json:"Id"`
			ExpireTime int64  `json:"ExpireTime"`
		} `json:"Token"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode Aliyun speech token: %w", err)
	}
	if result.Token.ID == "" || result.Token.ExpireTime <= now.Unix() {
		if result.ErrMsg != "" {
			return "", fmt.Errorf("Aliyun speech token rejected: %s", result.ErrMsg)
		}
		return "", errors.New("Aliyun speech token response is incomplete")
	}
	s.token = result.Token.ID
	s.expiresAt = time.Unix(result.Token.ExpireTime, 0)
	return s.token, nil
}

func aliyunPOPSignature(method string, parameters map[string]string, secret string) string {
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		if key != "Signature" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	canonical := make([]string, 0, len(keys))
	for _, key := range keys {
		canonical = append(
			canonical,
			aliyunPercentEncode(key)+"="+aliyunPercentEncode(parameters[key]),
		)
	}
	stringToSign := strings.ToUpper(method) + "&%2F&" +
		aliyunPercentEncode(strings.Join(canonical, "&"))
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	_, _ = mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func aliyunPercentEncode(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "*", "%2A")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	return encoded
}
