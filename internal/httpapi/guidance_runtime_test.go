package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/owui-personal-slim/owui-personal-slim/internal/config"
	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func TestUnassignedUserCannotActivateRestaurantWorkbench(t *testing.T) {
	ctx := context.Background()
	dataStore, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	user, err := dataStore.CreateUser(ctx, "general-owner", "General Owner", "hash")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		cfg:   config.Config{Tools: config.Tools{RestaurantGuidanceEnabled: true}},
		store: dataStore,
	}
	for _, body := range []string{
		`{"workbench":"restaurant"}`,
		`{"workbench":" RESTAURANT "}`,
	} {
		request := httptest.NewRequest(
			http.MethodPut,
			"/api/v1/me/workbench",
			strings.NewReader(body),
		)
		request = request.WithContext(withSession(request.Context(), store.Session{User: user}))
		response := httptest.NewRecorder()
		server.updateMyWorkbench(response, request)
		if response.Code != http.StatusForbidden ||
			!strings.Contains(response.Body.String(), "workbench_not_assigned") {
			t.Fatalf(
				"unassigned workbench update body=%s status=%d response=%s",
				body,
				response.Code,
				response.Body.String(),
			)
		}
	}
	setting, err := dataStore.WorkbenchSetting(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if setting.Initial != guidance.WorkbenchGeneral ||
		setting.Effective != guidance.WorkbenchGeneral ||
		setting.Preference != "" {
		t.Fatalf("forbidden update changed workbench setting: %#v", setting)
	}

	// Keep the response path defensive even if another internal caller writes
	// a restaurant preference for a generally assigned account.
	if _, err := dataStore.SetWorkbenchPreference(
		ctx, user.ID, guidance.WorkbenchRestaurant,
	); err != nil {
		t.Fatal(err)
	}
	runtime, err := server.guidanceRuntime(ctx, user.ID, nil, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.Enabled {
		t.Fatalf("unassigned account received restaurant guidance: %#v", runtime)
	}
}

func TestGuidanceRuntimeEnforcesTwoRoundsFiveQuestionsAndExplicitFinal(t *testing.T) {
	ctx := context.Background()
	dataStore, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	user, err := dataStore.CreateUser(ctx, "runtime-owner", "Runtime Owner", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetInitialWorkbenchByUsername(
		ctx, user.Username, guidance.WorkbenchRestaurant, "",
	); err != nil {
		t.Fatal(err)
	}
	server := &Server{
		cfg:   config.Config{Tools: config.Tools{RestaurantGuidanceEnabled: true}},
		store: dataStore,
	}
	messages := []store.Message{
		{
			ID: "user_start", Role: "user", Status: "completed",
			Parts: []store.MessagePart{{Type: "text", TextContent: "设计会员体系"}},
		},
		clarificationRuntimeMessage(t, "assistant_round_1", 3),
		submissionRuntimeMessage(t, "user_round_1", guidance.IntentContinueRefining),
	}
	runtime, err := server.guidanceRuntime(ctx, user.ID, messages, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.AllowClarification || runtime.MaxQuestions != 2 ||
		runtime.QuestionCount != 3 || runtime.RoundCount != 1 {
		t.Fatalf("runtime after first round = %#v", runtime)
	}

	messages = append(
		messages,
		clarificationRuntimeMessage(t, "assistant_round_2", 2),
		submissionRuntimeMessage(t, "user_round_2", guidance.IntentContinueRefining),
	)
	runtime, err = server.guidanceRuntime(ctx, user.ID, messages, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.AllowClarification || !runtime.AllowTaskBrief ||
		runtime.QuestionCount != 5 || runtime.RoundCount != 2 {
		t.Fatalf("runtime after second round = %#v", runtime)
	}

	messages = append(
		messages,
		taskBriefRuntimeMessage(t, "assistant_brief"),
		submissionRuntimeMessage(t, "user_more_context", guidance.IntentAddContext),
	)
	runtime, err = server.guidanceRuntime(ctx, user.ID, messages, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.AllowClarification || runtime.MaxQuestions != 3 ||
		!runtime.UserRequestedExtra {
		t.Fatalf("runtime after explicit additional context = %#v", runtime)
	}

	messages = append(
		messages,
		taskBriefRuntimeMessage(t, "assistant_brief_final"),
		submissionRuntimeMessage(t, "user_confirmed", guidance.IntentConfirmBrief),
	)
	runtime, err = server.guidanceRuntime(ctx, user.ID, messages, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.FinalAnswer || runtime.AllowClarification || runtime.AllowTaskBrief {
		t.Fatalf("runtime after brief confirmation = %#v", runtime)
	}
}

func TestGuidanceRuntimeKeepsQuestionBudgetForManualCardReplies(t *testing.T) {
	ctx := context.Background()
	dataStore, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	user, err := dataStore.CreateUser(ctx, "manual-guidance", "Manual Guidance", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetInitialWorkbenchByUsername(
		ctx, user.Username, guidance.WorkbenchRestaurant, "",
	); err != nil {
		t.Fatal(err)
	}
	server := &Server{
		cfg:   config.Config{Tools: config.Tools{RestaurantGuidanceEnabled: true}},
		store: dataStore,
	}
	messages := []store.Message{
		{
			ID: "manual_task", Role: "user", Status: "completed",
			Parts: []store.MessagePart{{Type: "text", TextContent: "设计会员体系"}},
		},
		clarificationRuntimeMessage(t, "manual_round_1", 3),
		{
			ID: "manual_reply", Role: "user", Status: "completed",
			Parts: []store.MessagePart{{
				Type: "text", TextContent: "主要想增加复购，顾客以家庭聚餐为主",
			}},
		},
	}
	runtime, err := server.guidanceRuntime(ctx, user.ID, messages, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.RoundCount != 1 ||
		runtime.QuestionCount != 3 ||
		!runtime.AllowClarification ||
		runtime.MaxQuestions != 2 {
		t.Fatalf("manual clarification reply reset the task budget: %#v", runtime)
	}

	messages = append(
		messages,
		clarificationRuntimeMessage(t, "manual_round_2", 2),
		store.Message{
			ID: "manual_round_2_reply", Role: "user", Status: "completed",
			Parts: []store.MessagePart{{
				Type: "text", TextContent: "优惠保守一些，先给我三个月试行版",
			}},
		},
		taskBriefRuntimeMessage(t, "manual_brief"),
		store.Message{
			ID: "manual_brief_reply", Role: "user", Status: "completed",
			Parts: []store.MessagePart{{
				Type: "text", TextContent: "再补充一点：收银系统只支持充值余额",
			}},
		},
	)
	runtime, err = server.guidanceRuntime(ctx, user.ID, messages, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !runtime.UserRequestedExtra ||
		!runtime.AllowClarification ||
		runtime.MaxQuestions != 3 {
		t.Fatalf("manual task-brief supplement was not treated as explicit extra context: %#v", runtime)
	}
}

func TestGuidanceRuntimeStopsOrdinaryQuestionsAfterDelegatedDefault(t *testing.T) {
	ctx := context.Background()
	dataStore, err := store.Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dataStore.Close() })
	user, err := dataStore.CreateUser(ctx, "delegated-guidance", "Delegated Guidance", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetInitialWorkbenchByUsername(
		ctx, user.Username, guidance.WorkbenchRestaurant, "",
	); err != nil {
		t.Fatal(err)
	}
	server := &Server{
		cfg:   config.Config{Tools: config.Tools{RestaurantGuidanceEnabled: true}},
		store: dataStore,
	}
	messages := []store.Message{
		{
			ID: "delegated_task", Role: "user", Status: "completed",
			Parts: []store.MessagePart{{Type: "text", TextContent: "设计会员体系"}},
		},
		clarificationRuntimeMessage(t, "delegated_cards", 2),
		delegatedSubmissionRuntimeMessage(t, "delegated_submission"),
	}
	runtime, err := server.guidanceRuntime(ctx, user.ID, messages, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.AllowClarification ||
		!runtime.AllowTaskBrief ||
		runtime.FinalAnswer {
		t.Fatalf("delegated default did not stop ordinary clarification: %#v", runtime)
	}
}

func TestBuildResponsesRequestReplaysNormalizedGuidanceAndExposesOnlyAllowedTools(t *testing.T) {
	server := &Server{cfg: config.Config{Tools: config.Tools{}}}
	runtime := guidance.Runtime{
		Enabled: true, AllowClarification: true, AllowTaskBrief: true,
		MaxQuestions: 2,
	}
	request, err := server.buildResponsesRequest(
		context.Background(),
		"user_1",
		store.Conversation{Model: "gpt-test"},
		provider.Model{ID: "gpt-test"},
		"high",
		nil,
		[]store.Message{
			{
				Role: "assistant", Status: "completed",
				Parts: []store.MessagePart{{
					Type:        guidance.PartClarification,
					TextContent: "需要确认：\n1. 首要目标是什么？",
				}},
			},
			{
				Role: "user", Status: "completed",
				Parts: []store.MessagePart{{
					Type:        guidance.PartClarificationSubmission,
					TextContent: "本轮需求补充：增加复购",
				}},
			},
		},
		runtime,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(request.Instructions, "Restaurant workbench") ||
		len(request.Input) != 2 {
		t.Fatalf("compiled guidance request = %#v", request)
	}
	if got := request.Input[0].Content.([]provider.ResponseContent)[0].Text; got != "需要确认：\n1. 首要目标是什么？" {
		t.Fatalf("assistant normalized guidance history = %q", got)
	}
	names := make([]string, 0)
	for _, tool := range request.Tools {
		if name, _ := tool["name"].(string); name != "" {
			names = append(names, name)
		}
	}
	if len(names) != 2 ||
		names[0] != guidance.ToolShowClarificationCards ||
		names[1] != guidance.ToolShowTaskBrief {
		t.Fatalf("guidance tool names = %v", names)
	}

	runtime.FinalAnswer = true
	runtime.AllowClarification = false
	runtime.AllowTaskBrief = false
	request, err = server.buildResponsesRequest(
		context.Background(), "user_1",
		store.Conversation{Model: "gpt-test"},
		provider.Model{ID: "gpt-test"}, "high", nil, nil, runtime,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Tools) != 0 ||
		!strings.Contains(request.Instructions, "Produce the complete answer now") {
		t.Fatalf("final-answer request = %#v", request)
	}
}

func TestBuildResponsesRequestKeepsGuidanceTextAlongsideSafeProviderItems(t *testing.T) {
	server := &Server{cfg: config.Config{Tools: config.Tools{}}}
	request, err := server.buildResponsesRequest(
		context.Background(),
		"user_1",
		store.Conversation{Model: "gpt-test"},
		provider.Model{ID: "gpt-test"},
		"high",
		nil,
		[]store.Message{{
			Role: "assistant", Status: "completed",
			ProviderItems: []store.ProviderItem{{
				ItemType: "web_search_call",
				ReplayJSON: json.RawMessage(`{
					"id":"search_1","type":"web_search_call","status":"completed"
				}`),
			}},
			Parts: []store.MessagePart{{
				Type:        guidance.PartClarification,
				TextContent: "需要确认：\n1. 首要目标是什么？",
			}},
		}},
		guidance.Runtime{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Input) != 2 || len(request.Input[0].Raw) == 0 {
		t.Fatalf("provider replay and guidance history = %#v", request.Input)
	}
	content, ok := request.Input[1].Content.([]provider.ResponseContent)
	if !ok || len(content) != 1 ||
		content[0].Text != "需要确认：\n1. 首要目标是什么？" {
		t.Fatalf("normalized guidance history beside provider item = %#v", request.Input[1])
	}
}

func clarificationRuntimeMessage(t *testing.T, id string, count int) store.Message {
	t.Helper()
	cards := guidance.ClarificationCards{
		SchemaVersion: guidance.SchemaVersion,
		InstanceID:    id,
	}
	for index := 0; index < count; index++ {
		cards.Questions = append(cards.Questions, guidance.ClarificationQuestion{
			Key:       "question_" + string(rune('a'+index)),
			Prompt:    "需要确认的问题",
			Selection: "single_select",
			Options: []guidance.ClarificationOption{
				{Key: "one", Label: "选项一"},
				{Key: "two", Label: "选项二"},
			},
			AllowOther: true, AllowDelegatedDefault: true,
		})
	}
	raw, err := json.Marshal(cards)
	if err != nil {
		t.Fatal(err)
	}
	return store.Message{
		ID: id, Role: "assistant", Status: "completed",
		Parts: []store.MessagePart{{
			Type: guidance.PartClarification, JSONContent: raw, TextContent: "澄清问题",
		}},
	}
}

func taskBriefRuntimeMessage(t *testing.T, id string) store.Message {
	t.Helper()
	brief := guidance.TaskBrief{
		SchemaVersion: guidance.SchemaVersion,
		InstanceID:    id, Goal: "设计会员体系",
		DesiredOutput: []string{"充值档位与规则"},
	}
	raw, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	return store.Message{
		ID: id, Role: "assistant", Status: "completed",
		Parts: []store.MessagePart{{
			Type: guidance.PartTaskBrief, JSONContent: raw, TextContent: "任务简报",
		}},
	}
}

func submissionRuntimeMessage(t *testing.T, id string, intent string) store.Message {
	t.Helper()
	submission := guidance.StoredSubmission{
		SchemaVersion:            guidance.SchemaVersion,
		SourceAssistantMessageID: "assistant_source",
		SourcePartID:             "part_source",
		Intent:                   intent,
	}
	raw, err := json.Marshal(submission)
	if err != nil {
		t.Fatal(err)
	}
	return store.Message{
		ID: id, Role: "user", Status: "completed",
		Parts: []store.MessagePart{{
			Type:        guidance.PartClarificationSubmission,
			JSONContent: raw, TextContent: "用户提交",
		}},
	}
}

func delegatedSubmissionRuntimeMessage(t *testing.T, id string) store.Message {
	t.Helper()
	submission := guidance.StoredSubmission{
		SchemaVersion:            guidance.SchemaVersion,
		SourceAssistantMessageID: "assistant_source",
		SourcePartID:             "part_source",
		Intent:                   guidance.IntentContinueRefining,
		Answers: []guidance.ClarificationAnswer{{
			QuestionKey: "goal", DelegatedDefault: true,
		}},
	}
	raw, err := json.Marshal(submission)
	if err != nil {
		t.Fatal(err)
	}
	return store.Message{
		ID: id, Role: "user", Status: "completed",
		Parts: []store.MessagePart{{
			Type:        guidance.PartClarificationSubmission,
			JSONContent: raw,
			TextContent: "用户选择你帮我决定",
		}},
	}
}
