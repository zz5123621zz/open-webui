package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/speech"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func TestHermesRestaurantBridgeClarificationFinalAudioAndIdempotency(t *testing.T) {
	finalText := "## 完整菜单\n\n1. 红烧肉：中式复古风。\n2. 清蒸鱼：适合家庭聚餐。"
	cpa := newHermesScriptedCPA(
		t,
		func(w http.ResponseWriter, _ int) {
			writeGuidanceFunctionStream(
				w,
				"resp_wechat_round_1",
				guidance.ToolShowClarificationCards,
				hermesCardsArguments("first"),
			)
		},
		func(w http.ResponseWriter, _ int) {
			writeGuidanceFunctionStream(
				w,
				"resp_wechat_round_2",
				guidance.ToolShowClarificationCards,
				hermesCardsArguments("second"),
			)
		},
		func(w http.ResponseWriter, _ int) {
			writeCompletedTextStream(w, "resp_wechat_final", finalText)
		},
	)
	tts := newHermesTestSpeechProvider()
	fixture := startHermesBridgeFixture(
		t,
		cpa.server.URL,
		tts,
		true,
		true,
	)

	first := postHermesTurn(
		t,
		fixture.server.URL,
		fixture.token,
		hermesRestaurantTurnRequest{
			RequestID: "wechat-request-1",
			SessionID: "wechat-session",
			Text:      "为我设计 20 道菜品",
		},
	)
	if first.status != http.StatusOK ||
		first.response.Kind != "clarification" ||
		first.response.Audio.Status != "not_applicable" {
		t.Fatalf("first turn = %#v", first)
	}
	for _, expected := range []string{
		"第 1/3 轮",
		"1.",
		"2.",
		"3.",
		"ABC",
		"直接生成",
	} {
		if !strings.Contains(first.response.Text, expected) {
			t.Errorf("first clarification lacks %q: %s", expected, first.response.Text)
		}
	}
	requests := cpa.snapshot()
	if len(requests) != 1 {
		t.Fatalf("CPA requests after first turn = %d", len(requests))
	}
	assertHermesQuestionSchema(t, requests[0], 3, 3)

	second := postHermesTurn(
		t,
		fixture.server.URL,
		fixture.token,
		hermesRestaurantTurnRequest{
			RequestID: "wechat-request-2",
			SessionID: "wechat-session",
			Text:      "ABC",
		},
	)
	if second.status != http.StatusOK ||
		second.response.Kind != "clarification" ||
		!strings.Contains(second.response.Text, "第 2/3 轮") {
		t.Fatalf("second turn = %#v", second)
	}
	requests = cpa.snapshot()
	if len(requests) != 2 {
		t.Fatalf("CPA requests after second turn = %d", len(requests))
	}
	assertHermesQuestionSchema(t, requests[1], 3, 3)
	if stringValue(requests[1]["tool_choice"]) != "required" {
		t.Fatalf("second CPA tool choice = %#v", requests[1]["tool_choice"])
	}

	finalRequest := hermesRestaurantTurnRequest{
		RequestID: "wechat-request-3",
		SessionID: "wechat-session",
		Text:      "1A 2B 3C，直接生成",
	}
	final := postHermesTurn(
		t,
		fixture.server.URL,
		fixture.token,
		finalRequest,
	)
	if final.status != http.StatusOK ||
		final.response.Kind != "answer" ||
		final.response.Text != finalText ||
		final.response.Audio.Status != "ready" ||
		len(final.response.Audio.Files) != 1 {
		t.Fatalf("final turn = %#v", final)
	}
	if cpa.responseCount() != 3 {
		t.Fatalf("CPA response count = %d", cpa.responseCount())
	}
	if tts.openCount() != 1 {
		t.Fatalf("TTS open count = %d", tts.openCount())
	}
	spoken := strings.Join(tts.allText(), "")
	if strings.Contains(spoken, "##") ||
		strings.Contains(spoken, "1.") ||
		!strings.Contains(spoken, "完整菜单") ||
		!strings.Contains(spoken, "红烧肉") {
		t.Fatalf("spoken text = %q", spoken)
	}

	audio := getHermesAudio(
		t,
		fixture.server.URL,
		fixture.token,
		final.response.Audio.Files[0].DownloadPath,
	)
	if audio.status != http.StatusOK ||
		audio.contentType != "audio/wav" ||
		!strings.HasPrefix(audio.disposition, `attachment; filename="answer-`) ||
		len(audio.body) < 48 ||
		string(audio.body[:4]) != "RIFF" ||
		string(audio.body[8:12]) != "WAVE" {
		t.Fatalf("audio download = %#v", audio)
	}

	repeated := postHermesTurn(
		t,
		fixture.server.URL,
		fixture.token,
		finalRequest,
	)
	if repeated.status != http.StatusOK ||
		repeated.response.Text != finalText ||
		len(repeated.response.Audio.Files) != 1 ||
		repeated.response.Audio.Files[0].ID != final.response.Audio.Files[0].ID {
		t.Fatalf("idempotent turn = %#v", repeated)
	}
	if cpa.responseCount() != 3 || tts.openCount() != 1 {
		t.Fatalf(
			"idempotent retry repeated work: CPA=%d TTS=%d",
			cpa.responseCount(),
			tts.openCount(),
		)
	}

	reused := postHermesTurn(
		t,
		fixture.server.URL,
		fixture.token,
		hermesRestaurantTurnRequest{
			RequestID: finalRequest.RequestID,
			SessionID: finalRequest.SessionID,
			Text:      "不同的输入",
		},
	)
	if reused.status != http.StatusConflict ||
		reused.errorCode != "request_id_reused" ||
		cpa.responseCount() != 3 {
		t.Fatalf("request ID reuse = %#v", reused)
	}

	otherToken := fixture.createCredential(t, "other-bridge-user")
	crossUserAudio := getHermesAudio(
		t,
		fixture.server.URL,
		otherToken,
		final.response.Audio.Files[0].DownloadPath,
	)
	if crossUserAudio.status != http.StatusNotFound {
		t.Fatalf("cross-user audio status = %d", crossUserAudio.status)
	}

	conversation, err := fixture.dataStore.HermesRestaurantConversation(
		context.Background(),
		fixture.credential,
		"wechat-session",
		"ignored",
		30,
	)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := fixture.dataStore.ListMessages(
		context.Background(),
		fixture.user.ID,
		conversation.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 6 ||
		messages[2].Parts[0].Type != guidance.PartClarificationSubmission ||
		messages[4].Parts[0].Type != guidance.PartClarificationSubmission ||
		!strings.Contains(messages[4].Parts[0].TextContent, "停止普通追问") {
		t.Fatalf("bridge transcript = %#v", messages)
	}
}

func TestHermesRestaurantBridgeNaturalAnswersAndTaskBrief(t *testing.T) {
	t.Run("natural answers", func(t *testing.T) {
		cpa := newHermesScriptedCPA(
			t,
			func(w http.ResponseWriter, _ int) {
				writeGuidanceFunctionStream(
					w,
					"resp_natural_cards",
					guidance.ToolShowClarificationCards,
					hermesCardsArguments("natural"),
				)
			},
			func(w http.ResponseWriter, _ int) {
				writeCompletedTextStream(
					w,
					"resp_natural_final",
					"自然语言答案已用于最终菜单。",
				)
			},
		)
		fixture := startHermesBridgeFixture(
			t, cpa.server.URL, nil, false, true,
		)
		first := postHermesTurn(
			t,
			fixture.server.URL,
			fixture.token,
			hermesRestaurantTurnRequest{
				RequestID: "natural-1",
				SessionID: "natural-session",
				Text:      "设计菜单",
			},
		)
		if first.response.Kind != "clarification" {
			t.Fatalf("first natural turn = %#v", first)
		}
		final := postHermesTurn(
			t,
			fixture.server.URL,
			fixture.token,
			hermesRestaurantTurnRequest{
				RequestID: "natural-2",
				SessionID: "natural-session",
				Text:      "复古，30 元左右，家常菜，直接生成",
			},
		)
		if final.status != http.StatusOK ||
			final.response.Kind != "answer" ||
			final.response.Audio.Status != "unavailable" ||
			final.response.Audio.Code != "speech_disabled" {
			t.Fatalf("natural final = %#v", final)
		}
		conversation, err := fixture.dataStore.HermesRestaurantConversation(
			context.Background(),
			fixture.credential,
			"natural-session",
			"ignored",
			30,
		)
		if err != nil {
			t.Fatal(err)
		}
		messages, err := fixture.dataStore.ListMessages(
			context.Background(),
			fixture.user.ID,
			conversation.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != 4 ||
			!strings.Contains(messages[2].Parts[0].TextContent, "复古") ||
			!strings.Contains(messages[2].Parts[0].TextContent, "30 元左右") ||
			!strings.Contains(messages[2].Parts[0].TextContent, "家常菜") {
			t.Fatalf("natural answer transcript = %#v", messages)
		}
	})

	t.Run("task brief current task only", func(t *testing.T) {
		cpa := newHermesScriptedCPA(
			t,
			func(w http.ResponseWriter, _ int) {
				writeGuidanceFunctionStream(
					w,
					"resp_bridge_brief",
					guidance.ToolShowTaskBrief,
					`{
						"schemaVersion":1,
						"goal":"设计 20 道中式菜品",
						"context":["面向家庭聚餐"],
						"constraints":["单道 30 元左右"],
						"desiredOutput":["完整菜单"],
						"delegatedAssumptions":[],
						"unresolved":[],
						"profileUpdateProposal":{
							"field":"cuisine_positioning",
							"operation":"set",
							"proposedValue":"中式复古",
							"reason":"可能是稳定定位"
						}
					}`,
				)
			},
			func(w http.ResponseWriter, _ int) {
				writeCompletedTextStream(
					w,
					"resp_bridge_brief_final",
					"已按任务简报生成完整菜单。",
				)
			},
		)
		fixture := startHermesBridgeFixture(
			t, cpa.server.URL, nil, false, true,
		)
		brief := postHermesTurn(
			t,
			fixture.server.URL,
			fixture.token,
			hermesRestaurantTurnRequest{
				RequestID: "brief-1",
				SessionID: "brief-session",
				Text:      "按现有资料设计菜单",
			},
		)
		if brief.status != http.StatusOK ||
			brief.response.Kind != "task_brief" ||
			brief.response.Audio.Status != "not_applicable" ||
			!strings.Contains(brief.response.Text, "仅把该信息用于本次任务") {
			t.Fatalf("task brief = %#v", brief)
		}
		final := postHermesTurn(
			t,
			fixture.server.URL,
			fixture.token,
			hermesRestaurantTurnRequest{
				RequestID: "brief-2",
				SessionID: "brief-session",
				Text:      "确认生成",
			},
		)
		if final.status != http.StatusOK ||
			final.response.Kind != "answer" ||
			final.response.Text != "已按任务简报生成完整菜单。" {
			t.Fatalf("confirmed brief = %#v", final)
		}
		conversation, err := fixture.dataStore.HermesRestaurantConversation(
			context.Background(),
			fixture.credential,
			"brief-session",
			"ignored",
			30,
		)
		if err != nil {
			t.Fatal(err)
		}
		messages, err := fixture.dataStore.ListMessages(
			context.Background(),
			fixture.user.ID,
			conversation.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(messages) != 4 ||
			!strings.Contains(
				messages[2].Parts[0].TextContent,
				"仅用于本次任务",
			) {
			t.Fatalf("task brief transcript = %#v", messages)
		}
		profile, err := fixture.dataStore.RestaurantProfile(
			context.Background(),
			fixture.user.ID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(profile) != 0 {
			t.Fatalf("profile was changed by current-task-only confirmation: %#v", profile)
		}
	})
}

func TestHermesRestaurantBridgeAuthenticationAndValidation(t *testing.T) {
	cpa := newHermesScriptedCPA(
		t,
		func(w http.ResponseWriter, _ int) {
			writeCompletedTextStream(w, "resp_validation", "有效请求")
		},
	)
	fixture := startHermesBridgeFixture(
		t, cpa.server.URL, nil, false, true,
	)
	endpoint := fixture.server.URL +
		"/api/v1/integrations/hermes/restaurant/turn"

	for name, authorization := range map[string]string{
		"missing": "",
		"wrong":   "Bearer hbr_" + strings.Repeat("A", 43),
		"basic":   "Basic credential",
	} {
		t.Run(name+" bearer", func(t *testing.T) {
			request, _ := http.NewRequest(
				http.MethodPost,
				endpoint,
				strings.NewReader(`{"requestId":"a","sessionId":"b","text":"c"}`),
			)
			request.Header.Set("Content-Type", "application/json")
			if authorization != "" {
				request.Header.Set("Authorization", authorization)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized {
				raw, _ := io.ReadAll(response.Body)
				t.Fatalf("status=%d body=%s", response.StatusCode, raw)
			}
		})
	}

	request, _ := http.NewRequest(
		http.MethodPost,
		endpoint,
		strings.NewReader(`{"requestId":"a","sessionId":"b","text":"c"}`),
	)
	request.Header.Add("Authorization", "Bearer "+fixture.token)
	request.Header.Add("Authorization", "Bearer "+fixture.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("duplicate authorization status=%d body=%s", response.StatusCode, raw)
	}
	response.Body.Close()

	request, _ = http.NewRequest(
		http.MethodPost,
		endpoint,
		strings.NewReader(`{"requestId":"a","sessionId":"b","text":"c"}`),
	)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	request.Header.Set("Content-Type", "text/plain")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnsupportedMediaType {
		raw, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("content type status=%d body=%s", response.StatusCode, raw)
	}
	response.Body.Close()

	invalidBodies := []struct {
		body string
		code string
	}{
		{`{"requestId":"a","sessionId":"b","text":"c","userId":"other"}`, "invalid_json"},
		{`{"requestId":"","sessionId":"b","text":"c"}`, "invalid_request_id"},
		{`{"requestId":"a","sessionId":"","text":"c"}`, "invalid_session_id"},
		{`{"requestId":"a","sessionId":"b","text":" "}`, "invalid_message"},
		{`{"requestId":"a\nb","sessionId":"b","text":"c"}`, "invalid_request_id"},
	}
	for _, test := range invalidBodies {
		got := rawHermesTurnRequest(
			t,
			endpoint,
			fixture.token,
			"application/json",
			test.body,
		)
		if got.status != http.StatusBadRequest || got.errorCode != test.code {
			t.Errorf("invalid body %s = %#v", test.body, got)
		}
	}
	oversized := hermesRestaurantTurnRequest{
		RequestID: "oversized",
		SessionID: "validation-session",
		Text:      strings.Repeat("菜", maxHermesTurnTextBytes/3+1),
	}
	got := postHermesTurn(
		t,
		fixture.server.URL,
		fixture.token,
		oversized,
	)
	if got.status != http.StatusBadRequest || got.errorCode != "invalid_message" {
		t.Fatalf("oversized message = %#v", got)
	}
	if cpa.responseCount() != 0 {
		t.Fatalf("invalid requests reached CPA %d times", cpa.responseCount())
	}

	if err := fixture.dataStore.RevokeHermesRestaurantCredential(
		context.Background(),
		fixture.credential.ID,
	); err != nil {
		t.Fatal(err)
	}
	revoked := postHermesTurn(
		t,
		fixture.server.URL,
		fixture.token,
		hermesRestaurantTurnRequest{
			RequestID: "revoked",
			SessionID: "validation-session",
			Text:      "不能执行",
		},
	)
	if revoked.status != http.StatusUnauthorized {
		t.Fatalf("revoked credential response = %#v", revoked)
	}
}

func TestHermesRestaurantBridgeLongAudioAndAllOrNothingFailure(t *testing.T) {
	t.Run("multiple ordered WAV files", func(t *testing.T) {
		answer := strings.Repeat("菜", speech.DefaultFileChunkRunes+100)
		cpa := newHermesScriptedCPA(
			t,
			func(w http.ResponseWriter, _ int) {
				writeCompletedTextStream(w, "resp_long_audio", answer)
			},
		)
		tts := newHermesTestSpeechProvider()
		fixture := startHermesBridgeFixture(
			t, cpa.server.URL, tts, true, true,
		)
		result := postHermesTurn(
			t,
			fixture.server.URL,
			fixture.token,
			hermesRestaurantTurnRequest{
				RequestID: "long-audio",
				SessionID: "long-audio-session",
				Text:      "直接生成长答案",
			},
		)
		if result.status != http.StatusOK ||
			result.response.Text != answer ||
			result.response.Audio.Status != "ready" ||
			len(result.response.Audio.Files) != 2 {
			t.Fatalf("long audio result = %#v", result)
		}
		if tts.openCount() != 2 {
			t.Fatalf("TTS sessions = %d", tts.openCount())
		}
		for index, file := range result.response.Audio.Files {
			expected := "answer-0" + string(rune('1'+index)) + "-of-02.wav"
			if file.FileName != expected {
				t.Errorf("audio file %d = %q, want %q", index, file.FileName, expected)
			}
			download := getHermesAudio(
				t,
				fixture.server.URL,
				fixture.token,
				file.DownloadPath,
			)
			if download.status != http.StatusOK ||
				string(download.body[:4]) != "RIFF" {
				t.Fatalf("audio %d download = %#v", index, download)
			}
		}
	})

	t.Run("later segment failure removes entire batch", func(t *testing.T) {
		answer := strings.Repeat("汤", speech.DefaultFileChunkRunes+100)
		cpa := newHermesScriptedCPA(
			t,
			func(w http.ResponseWriter, _ int) {
				writeCompletedTextStream(w, "resp_failed_audio", answer)
			},
		)
		tts := newHermesTestSpeechProvider()
		tts.failOpenAt = 2
		fixture := startHermesBridgeFixture(
			t, cpa.server.URL, tts, true, true,
		)
		result := postHermesTurn(
			t,
			fixture.server.URL,
			fixture.token,
			hermesRestaurantTurnRequest{
				RequestID: "failed-audio",
				SessionID: "failed-audio-session",
				Text:      "直接生成长答案",
			},
		)
		if result.status != http.StatusOK ||
			result.response.Text != answer ||
			result.response.Audio.Status != "unavailable" ||
			result.response.Audio.Code != "speech_provider_failed" ||
			len(result.response.Audio.Files) != 0 ||
			cpa.responseCount() != 1 {
			t.Fatalf("failed audio result = %#v", result)
		}
		audioRoot := filepath.Join(
			fixture.dataDir,
			"hermes-restaurant-audio",
		)
		entries, err := os.ReadDir(audioRoot)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("partial audio files remain: %#v", entries)
		}
		requestPrefix := hermesRestaurantRequestPrefix(
			fixture.credential.ID,
			"failed-audio",
		)
		requestKey := hermesRestaurantRequestKey(
			requestPrefix,
			"failed-audio-session",
			"直接生成长答案",
		)
		records, err := fixture.dataStore.HermesRestaurantAudioForRequest(
			context.Background(),
			fixture.credential.ID,
			fixture.user.ID,
			requestKey,
			time.Now().UnixMilli(),
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(records) != 0 {
			t.Fatalf("partial audio records = %#v", records)
		}
	})

	t.Run("excessive segment count degrades before TTS", func(t *testing.T) {
		answer := strings.Repeat(
			"菜",
			speech.DefaultFileChunkRunes*maxHermesRestaurantAudioFiles+1,
		)
		cpa := newHermesScriptedCPA(
			t,
			func(w http.ResponseWriter, _ int) {
				writeCompletedTextStream(w, "resp_excessive_audio", answer)
			},
		)
		tts := newHermesTestSpeechProvider()
		fixture := startHermesBridgeFixture(
			t, cpa.server.URL, tts, true, true,
		)
		result := postHermesTurn(
			t,
			fixture.server.URL,
			fixture.token,
			hermesRestaurantTurnRequest{
				RequestID: "excessive-audio",
				SessionID: "excessive-audio-session",
				Text:      "直接生成超长答案",
			},
		)
		if result.status != http.StatusOK ||
			result.response.Text != answer ||
			result.response.Audio.Status != "unavailable" ||
			result.response.Audio.Code != "speech_too_large" ||
			len(result.response.Audio.Files) != 0 ||
			tts.openCount() != 0 {
			t.Fatalf("excessive audio result = %#v", result)
		}
	})
}

func TestHermesRestaurantBridgeConcurrentTurnAndCompletedRetry(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	cpa := newHermesScriptedCPA(
		t,
		func(w http.ResponseWriter, _ int) {
			close(started)
			<-release
			writeCompletedTextStream(w, "resp_concurrent", "并发请求完成。")
		},
	)
	fixture := startHermesBridgeFixture(
		t, cpa.server.URL, nil, false, true,
	)
	request := hermesRestaurantTurnRequest{
		RequestID: "concurrent-request",
		SessionID: "concurrent-session",
		Text:      "直接生成",
	}
	firstResult := make(chan hermesHTTPResult, 1)
	go func() {
		firstResult <- doHermesTurn(
			fixture.server.URL,
			fixture.token,
			request,
		)
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("first CPA request did not start")
	}

	// More than the former 30/minute limit proves that idempotent recovery
	// polling cannot be cut off with a false rate-limit response.
	for attempt := 0; attempt < 35; attempt++ {
		second := postHermesTurn(
			t,
			fixture.server.URL,
			fixture.token,
			request,
		)
		if second.status != http.StatusConflict ||
			second.errorCode != "turn_in_progress" ||
			second.retryAfter != "2" {
			t.Fatalf(
				"concurrent poll %d = %#v",
				attempt+1,
				second,
			)
		}
	}
	releaseOnce.Do(func() { close(release) })
	var first hermesHTTPResult
	select {
	case first = <-firstResult:
	case <-time.After(5 * time.Second):
		t.Fatal("first concurrent request did not finish")
	}
	if first.err != nil ||
		first.status != http.StatusOK ||
		first.response.Text != "并发请求完成。" {
		t.Fatalf("first concurrent request = %#v", first)
	}
	if cpa.responseCount() != 1 {
		t.Fatalf("concurrent CPA calls = %d", cpa.responseCount())
	}

	retry := postHermesTurn(
		t,
		fixture.server.URL,
		fixture.token,
		request,
	)
	if retry.status != http.StatusOK ||
		retry.response.Text != "并发请求完成。" ||
		cpa.responseCount() != 1 {
		t.Fatalf("completed retry = %#v CPA=%d", retry, cpa.responseCount())
	}
}

func TestHermesRestaurantBridgeFailureBoundariesAndFeatureSwitch(t *testing.T) {
	t.Run("guidance disabled", func(t *testing.T) {
		cpa := newHermesScriptedCPA(t)
		fixture := startHermesBridgeFixture(
			t, cpa.server.URL, nil, false, false,
		)
		result := postHermesTurn(
			t,
			fixture.server.URL,
			fixture.token,
			hermesRestaurantTurnRequest{
				RequestID: "disabled",
				SessionID: "disabled-session",
				Text:      "设计菜单",
			},
		)
		if result.status != http.StatusConflict ||
			result.errorCode != "guidance_disabled" ||
			cpa.responseCount() != 0 {
			t.Fatalf("disabled guidance result = %#v", result)
		}
	})

	t.Run("provider details are not exposed", func(t *testing.T) {
		cpa := newHermesScriptedCPA(
			t,
			func(w http.ResponseWriter, _ int) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(
					w,
					`{"error":{"code":"upstream_failed","message":"SECRET_PROVIDER_DETAIL"}}`,
				)
			},
		)
		fixture := startHermesBridgeFixture(
			t, cpa.server.URL, nil, false, true,
		)
		result := postHermesTurn(
			t,
			fixture.server.URL,
			fixture.token,
			hermesRestaurantTurnRequest{
				RequestID: "provider-failure",
				SessionID: "provider-failure-session",
				Text:      "直接生成",
			},
		)
		if result.status != http.StatusBadGateway ||
			strings.Contains(string(result.raw), "SECRET_PROVIDER_DETAIL") {
			t.Fatalf("provider failure result = %#v", result)
		}
	})

	t.Run("service stopping", func(t *testing.T) {
		cpa := newHermesScriptedCPA(t)
		fixture := startHermesBridgeFixture(
			t, cpa.server.URL, nil, false, true,
		)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := fixture.app.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
		result := postHermesTurn(
			t,
			fixture.server.URL,
			fixture.token,
			hermesRestaurantTurnRequest{
				RequestID: "stopping",
				SessionID: "stopping-session",
				Text:      "直接生成",
			},
		)
		if result.status != http.StatusServiceUnavailable ||
			result.errorCode != "service_stopping" {
			t.Fatalf("service stopping result = %#v", result)
		}
	})
}

func TestHermesRestaurantAudioIntegrityAndPathProtection(t *testing.T) {
	cpa := newHermesScriptedCPA(
		t,
		func(w http.ResponseWriter, _ int) {
			writeCompletedTextStream(w, "resp_integrity", "需要语音的答案。")
		},
	)
	tts := newHermesTestSpeechProvider()
	fixture := startHermesBridgeFixture(
		t, cpa.server.URL, tts, true, true,
	)
	result := postHermesTurn(
		t,
		fixture.server.URL,
		fixture.token,
		hermesRestaurantTurnRequest{
			RequestID: "integrity",
			SessionID: "integrity-session",
			Text:      "直接生成",
		},
	)
	if result.response.Audio.Status != "ready" ||
		len(result.response.Audio.Files) != 1 {
		t.Fatalf("integrity setup result = %#v", result)
	}
	file := result.response.Audio.Files[0]
	record, err := fixture.dataStore.HermesRestaurantAudioByID(
		context.Background(),
		fixture.credential.ID,
		fixture.user.ID,
		file.ID,
		time.Now().UnixMilli(),
	)
	if err != nil {
		t.Fatal(err)
	}
	fullPath, ok := fixture.app.hermesAudioPath(record.StoragePath)
	if !ok {
		t.Fatalf("stored path rejected: %q", record.StoragePath)
	}
	contents, err := os.ReadFile(fullPath)
	if err != nil {
		t.Fatal(err)
	}
	contents[len(contents)-1] ^= 0xff
	if err := os.WriteFile(fullPath, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	tampered := getHermesAudio(
		t,
		fixture.server.URL,
		fixture.token,
		file.DownloadPath,
	)
	if tampered.status != http.StatusNotFound {
		t.Fatalf("tampered audio status = %d", tampered.status)
	}
	for _, path := range []string{
		"../outside.wav",
		"/tmp/outside.wav",
		"hermes-restaurant-audio/../../outside.wav",
		"hermes-restaurant-audio",
	} {
		if resolved, ok := fixture.app.hermesAudioPath(path); ok || resolved != "" {
			t.Errorf("path %q resolved to %q", path, resolved)
		}
	}
}

type hermesScript func(http.ResponseWriter, int)

type hermesScriptedCPA struct {
	server   *httptest.Server
	mu       sync.Mutex
	requests []map[string]any
	scripts  []hermesScript
}

func newHermesScriptedCPA(
	t *testing.T,
	scripts ...hermesScript,
) *hermesScriptedCPA {
	t.Helper()
	cpa := &hermesScriptedCPA{scripts: scripts}
	cpa.server = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/models":
				writeGuidanceTestModel(w)
			case "/v1/responses":
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				cpa.mu.Lock()
				cpa.requests = append(cpa.requests, request)
				index := len(cpa.requests) - 1
				var script hermesScript
				if index < len(cpa.scripts) {
					script = cpa.scripts[index]
				}
				cpa.mu.Unlock()
				if script == nil {
					http.Error(w, "unexpected CPA request", http.StatusInternalServerError)
					return
				}
				script(w, index)
			default:
				http.NotFound(w, r)
			}
		},
	))
	t.Cleanup(cpa.server.Close)
	return cpa
}

func (c *hermesScriptedCPA) snapshot() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]map[string]any(nil), c.requests...)
}

func (c *hermesScriptedCPA) responseCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.requests)
}

type hermesBridgeFixture struct {
	app        *Server
	server     *httptest.Server
	dataStore  *store.Store
	dataDir    string
	user       store.User
	credential store.HermesRestaurantCredential
	token      string
}

func startHermesBridgeFixture(
	t *testing.T,
	providerBaseURL string,
	speechProvider speech.Provider,
	speechEnabled bool,
	guidanceEnabled bool,
) *hermesBridgeFixture {
	t.Helper()
	ctx := context.Background()
	dataDir := t.TempDir()
	dataStore, err := store.Open(ctx, filepath.Join(dataDir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	user, err := dataStore.CreateUser(
		ctx,
		"hermes-bridge-user",
		"Hermes Bridge User",
		"hash",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetInitialWorkbenchByUsername(
		ctx,
		user.Username,
		guidance.WorkbenchRestaurant,
		"",
	); err != nil {
		t.Fatal(err)
	}
	credential, token, err := dataStore.CreateHermesRestaurantCredential(
		ctx,
		user.Username,
		"bridge-test",
		"gpt-guidance",
		"high",
	)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := dataStore.CreateUserWithRole(
		ctx,
		"hermes-bridge-admin",
		"Hermes Bridge Admin",
		"hash",
		"admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	if speechEnabled {
		if speechProvider == nil {
			t.Fatal("speechEnabled requires a test speech provider")
		}
		if _, err := dataStore.SetSpeechServiceSetting(
			ctx,
			admin.ID,
			store.SpeechServiceSetting{
				Enabled:      true,
				Provider:     speechProvider.ID(),
				DefaultVoice: "test-voice",
			},
		); err != nil {
			t.Fatal(err)
		}
	}

	baseURL, _ := url.Parse("http://chat.test")
	providerURL, _ := url.Parse(providerBaseURL + "/v1")
	cfg := config.Config{
		Environment:       "test",
		HTTPAddr:          ":0",
		BaseURL:           baseURL,
		DataDir:           dataDir,
		DatabasePath:      filepath.Join(dataDir, "app.db"),
		AppSecret:         []byte("01234567890123456789012345678901"),
		SessionTTL:        time.Hour,
		SessionCookieName: "owui_session",
		Provider: config.Provider{
			Kind:                      "cpa",
			BaseURL:                   providerURL,
			APIKey:                    "provider-test-key",
			DefaultModel:              "gpt-guidance",
			ModelsTimeout:             time.Second,
			DefaultReasoningEffort:    "high",
			UnknownModelContextTokens: 128000,
			RequestBodyMaxBytes:       50 << 20,
		},
		Speech: config.Speech{
			MaxConcurrentGlobal:  2,
			MaxConcurrentPerUser: 1,
			SessionTTL:           5 * time.Second,
		},
		Jobs: config.Jobs{
			MaxConcurrentGlobal:  2,
			MaxConcurrentPerUser: 1,
			MaxQueuedPerUser:     1,
			QueueTimeout:         time.Second,
		},
		Tools: config.Tools{
			RestaurantGuidanceEnabled: guidanceEnabled,
		},
		Lifecycle: config.Lifecycle{
			MaxActiveConversations: 30,
		},
	}
	app := New(
		cfg,
		dataStore,
		provider.NewClient(cfg.Provider, "test"),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if speechProvider != nil {
		app.speechProviders = speech.NewRegistry(speechProvider)
	}
	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer cancel()
		_ = app.Shutdown(shutdownContext)
	})
	return &hermesBridgeFixture{
		app: app, server: server, dataStore: dataStore, dataDir: dataDir,
		user: user, credential: credential, token: token,
	}
}

func (f *hermesBridgeFixture) createCredential(
	t *testing.T,
	username string,
) string {
	t.Helper()
	_, err := f.dataStore.CreateUser(
		context.Background(),
		username,
		username,
		"hash",
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.dataStore.SetInitialWorkbenchByUsername(
		context.Background(),
		username,
		guidance.WorkbenchRestaurant,
		"",
	); err != nil {
		t.Fatal(err)
	}
	_, token, err := f.dataStore.CreateHermesRestaurantCredential(
		context.Background(),
		username,
		"other",
		"gpt-guidance",
		"high",
	)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

type hermesHTTPResult struct {
	status     int
	retryAfter string
	raw        []byte
	response   hermesRestaurantTurnResponse
	errorCode  string
	err        error
}

func postHermesTurn(
	t *testing.T,
	baseURL string,
	token string,
	request hermesRestaurantTurnRequest,
) hermesHTTPResult {
	t.Helper()
	result := doHermesTurn(baseURL, token, request)
	if result.err != nil {
		t.Fatal(result.err)
	}
	return result
}

func doHermesTurn(
	baseURL string,
	token string,
	request hermesRestaurantTurnRequest,
) hermesHTTPResult {
	raw, err := json.Marshal(request)
	if err != nil {
		return hermesHTTPResult{err: err}
	}
	endpoint := baseURL + "/api/v1/integrations/hermes/restaurant/turn"
	httpRequest, err := http.NewRequest(
		http.MethodPost,
		endpoint,
		bytes.NewReader(raw),
	)
	if err != nil {
		return hermesHTTPResult{err: err}
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+token)
	}
	httpResponse, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		return hermesHTTPResult{err: err}
	}
	defer httpResponse.Body.Close()
	body, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return hermesHTTPResult{err: err}
	}
	result := hermesHTTPResult{
		status: httpResponse.StatusCode, raw: body,
		retryAfter: httpResponse.Header.Get("Retry-After"),
	}
	if httpResponse.StatusCode == http.StatusOK {
		result.err = json.Unmarshal(body, &result.response)
		return result
	}
	var errorPayload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &errorPayload); err == nil {
		result.errorCode = errorPayload.Error.Code
	}
	return result
}

func rawHermesTurnRequest(
	t *testing.T,
	endpoint string,
	token string,
	contentType string,
	body string,
) hermesHTTPResult {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		endpoint,
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", contentType)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	result := hermesHTTPResult{status: response.StatusCode, raw: raw}
	var errorPayload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &errorPayload); err == nil {
		result.errorCode = errorPayload.Error.Code
	}
	return result
}

type hermesAudioDownload struct {
	status      int
	contentType string
	disposition string
	body        []byte
}

func getHermesAudio(
	t *testing.T,
	baseURL string,
	token string,
	downloadPath string,
) hermesAudioDownload {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodGet,
		baseURL+downloadPath,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return hermesAudioDownload{
		status: response.StatusCode, body: body,
		contentType: response.Header.Get("Content-Type"),
		disposition: response.Header.Get("Content-Disposition"),
	}
}

func assertHermesQuestionSchema(
	t *testing.T,
	request map[string]any,
	minimum int,
	maximum int,
) {
	t.Helper()
	rawTools, ok := request["tools"].([]any)
	if !ok {
		t.Fatalf("CPA tools = %#v", request["tools"])
	}
	for _, rawTool := range rawTools {
		tool, _ := rawTool.(map[string]any)
		if tool["name"] != guidance.ToolShowClarificationCards {
			continue
		}
		parameters, _ := tool["parameters"].(map[string]any)
		properties, _ := parameters["properties"].(map[string]any)
		questions, _ := properties["questions"].(map[string]any)
		if int(questions["minItems"].(float64)) != minimum ||
			int(questions["maxItems"].(float64)) != maximum {
			t.Fatalf("question schema = %#v", questions)
		}
		return
	}
	t.Fatal("clarification tool not found")
}

func hermesCardsArguments(prefix string) string {
	return `{
		"schemaVersion":1,
		"intro":"请确认三个关键点。",
		"currentUnderstanding":["需要设计 20 道菜品"],
		"questions":[{
			"key":"` + prefix + `_style",
			"prompt":"菜品风格是什么？",
			"selection":"single_select",
			"options":[
				{"key":"retro","label":"复古","description":null},
				{"key":"western","label":"西餐","description":null},
				{"key":"home","label":"家常菜","description":null}
			],
			"allowOther":true,
			"allowDelegatedDefault":true,
			"minimumSelections":1,
			"maximumSelections":1
		},{
			"key":"` + prefix + `_price",
			"prompt":"价位如何？",
			"selection":"single_select",
			"options":[
				{"key":"low","label":"20 元以内","description":null},
				{"key":"mid","label":"30 元左右","description":null},
				{"key":"high","label":"50 元左右","description":null}
			],
			"allowOther":true,
			"allowDelegatedDefault":true,
			"minimumSelections":1,
			"maximumSelections":1
		},{
			"key":"` + prefix + `_occasion",
			"prompt":"主要场景是什么？",
			"selection":"single_select",
			"options":[
				{"key":"daily","label":"日常散客","description":null},
				{"key":"business","label":"商务宴请","description":null},
				{"key":"family","label":"家常菜","description":null}
			],
			"allowOther":true,
			"allowDelegatedDefault":true,
			"minimumSelections":1,
			"maximumSelections":1
		}]
	}`
}

type hermesTestSpeechProvider struct {
	mu         sync.Mutex
	sessions   []*hermesTestSpeechSession
	openCalls  int
	failOpenAt int
}

func newHermesTestSpeechProvider() *hermesTestSpeechProvider {
	return &hermesTestSpeechProvider{}
}

func (p *hermesTestSpeechProvider) ID() string       { return "hermes-test-speech" }
func (p *hermesTestSpeechProvider) Configured() bool { return true }
func (p *hermesTestSpeechProvider) Voices() []speech.Voice {
	return []speech.Voice{{ID: "test-voice", Label: "Test Voice"}}
}
func (p *hermesTestSpeechProvider) Open(
	_ context.Context,
	config speech.SessionConfig,
) (speech.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.openCalls++
	if p.failOpenAt > 0 && p.openCalls == p.failOpenAt {
		return nil, errors.New("synthetic speech failure")
	}
	session := &hermesTestSpeechSession{requested: config}
	p.sessions = append(p.sessions, session)
	return session, nil
}
func (p *hermesTestSpeechProvider) openCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.openCalls
}
func (p *hermesTestSpeechProvider) allText() []string {
	p.mu.Lock()
	sessions := append([]*hermesTestSpeechSession(nil), p.sessions...)
	p.mu.Unlock()
	var result []string
	for _, session := range sessions {
		session.mu.Lock()
		result = append(result, session.sent...)
		session.mu.Unlock()
	}
	return result
}

type hermesTestSpeechSession struct {
	mu        sync.Mutex
	requested speech.SessionConfig
	sent      []string
	finished  bool
	readIndex int
}

func (s *hermesTestSpeechSession) AudioConfig() speech.AudioConfig {
	return speech.AudioConfig{
		Format: "pcm", SampleRate: 24000, Channels: 1, BitDepth: 16,
	}
}
func (s *hermesTestSpeechSession) SendText(
	_ context.Context,
	value string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, value)
	return nil
}
func (s *hermesTestSpeechSession) Finish(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.finished = true
	return nil
}
func (s *hermesTestSpeechSession) ReadEvent(
	context.Context,
) (speech.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.finished {
		return speech.Event{}, errors.New("read before finish")
	}
	s.readIndex++
	if s.readIndex == 1 {
		return speech.Event{
			Type:  speech.EventAudio,
			Audio: []byte{1, 2, 3, 4},
		}, nil
	}
	return speech.Event{Type: speech.EventCompleted}, nil
}
func (s *hermesTestSpeechSession) Close() error { return nil }
