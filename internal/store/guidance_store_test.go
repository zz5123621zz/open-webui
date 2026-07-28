package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
)

func TestWorkbenchPreferenceOverridesAuditedInitialAssignment(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "restaurant-user", "Restaurant User", "hash")
	if err != nil {
		t.Fatal(err)
	}
	setting, err := dataStore.SetInitialWorkbenchByUsername(
		ctx, user.Username, guidance.WorkbenchRestaurant, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if setting.Initial != guidance.WorkbenchRestaurant ||
		setting.Effective != guidance.WorkbenchRestaurant {
		t.Fatalf("initial restaurant workbench = %#v", setting)
	}
	setting, err = dataStore.SetWorkbenchPreference(
		ctx, user.ID, guidance.WorkbenchGeneral,
	)
	if err != nil {
		t.Fatal(err)
	}
	if setting.Preference != guidance.WorkbenchGeneral ||
		setting.Effective != guidance.WorkbenchGeneral {
		t.Fatalf("general preference did not override initial assignment: %#v", setting)
	}
	if _, err := dataStore.SetInitialWorkbenchByUsername(
		ctx, user.Username, guidance.WorkbenchGeneral, "",
	); err != nil {
		t.Fatal(err)
	}
	setting, err = dataStore.SetInitialWorkbenchByUsername(
		ctx, user.Username, guidance.WorkbenchRestaurant, "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if setting.Initial != guidance.WorkbenchRestaurant ||
		setting.Preference != guidance.WorkbenchGeneral ||
		setting.Effective != guidance.WorkbenchGeneral {
		t.Fatalf("administrator assignment overrode user preference: %#v", setting)
	}
	var audits int
	if err := dataStore.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM workbench_assignment_audit WHERE user_id = ?
	`, user.ID).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 3 {
		t.Fatalf("workbench assignment audit count = %d, want 3", audits)
	}
}

func TestBeginGuidanceResponseIsAtomicIdempotentAndRejectsStaleCards(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "guidance-owner", "Guidance Owner", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetInitialWorkbenchByUsername(
		ctx, user.Username, guidance.WorkbenchRestaurant, "",
	); err != nil {
		t.Fatal(err)
	}
	other, err := dataStore.CreateUser(ctx, "guidance-other", "Guidance Other", "hash")
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := dataStore.CreateConversation(
		ctx, user.ID, "Restaurant", "gpt-test", "high",
	)
	if err != nil {
		t.Fatal(err)
	}
	source := completeClarificationSource(t, dataStore, user.ID, conversation.ID)
	results, err := dataStore.SearchConversations(ctx, user.ID, "增加复购", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Conversation.ID != conversation.ID {
		t.Fatalf("structured clarification search results = %#v", results)
	}
	submission := guidance.GuidanceSubmission{
		SourceAssistantMessageID: source.ID,
		SourcePartID:             source.Parts[0].ID,
		Intent:                   guidance.IntentContinueRefining,
		Answers: []guidance.ClarificationAnswer{
			{QuestionKey: "goal", SelectedOptionKeys: []string{"repeat"}},
			{QuestionKey: "audience", DelegatedDefault: true},
		},
	}
	userMessage, assistant, err := dataStore.BeginGuidanceResponse(
		ctx, user.ID, conversation.ID, "guidance-request-1", submission,
		"gpt-test", "high", "high",
	)
	if err != nil {
		t.Fatal(err)
	}
	if assistant.ParentMessageID != userMessage.ID ||
		userMessage.Parts[0].Type != guidance.PartClarificationSubmission ||
		assistant.Status != "streaming" {
		t.Fatalf("guidance response messages = user %#v assistant %#v", userMessage, assistant)
	}
	if _, _, err := dataStore.BeginGuidanceResponse(
		ctx, user.ID, conversation.ID, "guidance-request-1", submission,
		"gpt-test", "high", "high",
	); !errors.Is(err, ErrDuplicateRequest) {
		t.Fatalf("duplicate guidance submission error = %v", err)
	}
	if _, _, err := dataStore.BeginGuidanceResponse(
		ctx, user.ID, conversation.ID, "guidance-request-2", submission,
		"gpt-test", "high", "high",
	); !errors.Is(err, ErrStaleGuidance) {
		t.Fatalf("stale guidance submission error = %v", err)
	}
	if _, _, err := dataStore.BeginGuidanceResponse(
		ctx, other.ID, conversation.ID, "guidance-request-3", submission,
		"gpt-test", "high", "high",
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user guidance submission error = %v", err)
	}
}

func TestConfirmedProfileUpdateSurvivesSourceConversationDeletion(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "profile-owner", "Profile Owner", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetInitialWorkbenchByUsername(
		ctx, user.Username, guidance.WorkbenchRestaurant, "",
	); err != nil {
		t.Fatal(err)
	}
	conversation, err := dataStore.CreateConversation(
		ctx, user.ID, "Restaurant profile", "gpt-test", "high",
	)
	if err != nil {
		t.Fatal(err)
	}
	source := completeTaskBriefSource(t, dataStore, user.ID, conversation.ID)
	_, _, err = dataStore.BeginGuidanceResponse(
		ctx, user.ID, conversation.ID, "profile-guidance-request",
		guidance.GuidanceSubmission{
			SourceAssistantMessageID: source.ID,
			SourcePartID:             source.Parts[0].ID,
			Intent:                   guidance.IntentConfirmBrief,
			ProfileDecision:          guidance.ProfileDecisionSave,
		},
		"gpt-test", "high", "high",
	)
	if err != nil {
		t.Fatal(err)
	}
	facts, err := dataStore.RestaurantProfile(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 ||
		facts[0].Field != "primary_customers" ||
		facts[0].Value != "附近家庭聚餐顾客" {
		t.Fatalf("restaurant profile = %#v", facts)
	}
	if _, err := dataStore.db.ExecContext(
		ctx, `DELETE FROM conversations WHERE id = ?`, conversation.ID,
	); err != nil {
		t.Fatal(err)
	}
	facts, err = dataStore.RestaurantProfile(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 1 || facts[0].Value != "附近家庭聚餐顾客" {
		t.Fatalf("profile was removed with its source conversation: %#v", facts)
	}
	var factSource, auditSource any
	if err := dataStore.db.QueryRowContext(ctx, `
		SELECT source_message_id FROM restaurant_profile_facts
		WHERE user_id = ? AND field_key = 'primary_customers'
	`, user.ID).Scan(&factSource); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.db.QueryRowContext(ctx, `
		SELECT source_message_id FROM restaurant_profile_audit
		WHERE user_id = ? AND field_key = 'primary_customers'
	`, user.ID).Scan(&auditSource); err != nil {
		t.Fatal(err)
	}
	if factSource != nil || auditSource != nil {
		t.Fatalf("deleted source references = fact %#v audit %#v, want NULL", factSource, auditSource)
	}
}

func TestRestaurantProfileReplaceDeleteAndCurrentTaskOnly(t *testing.T) {
	ctx := context.Background()
	dataStore := openTestStore(t)
	user, err := dataStore.CreateUser(ctx, "profile-lifecycle", "Profile Lifecycle", "hash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.SetInitialWorkbenchByUsername(
		ctx, user.Username, guidance.WorkbenchRestaurant, "",
	); err != nil {
		t.Fatal(err)
	}
	submitProposal := func(
		suffix string,
		proposal guidance.ProfileUpdateProposal,
		decision string,
	) {
		t.Helper()
		conversation, err := dataStore.CreateConversation(
			ctx, user.ID, "Profile "+suffix, "gpt-test", "high",
		)
		if err != nil {
			t.Fatalf("create %s conversation: %v", suffix, err)
		}
		source := completeTaskBriefProposalSource(
			t,
			dataStore,
			user.ID,
			conversation.ID,
			"profile-source-"+suffix,
			proposal,
		)
		if _, _, err := dataStore.BeginGuidanceResponse(
			ctx,
			user.ID,
			conversation.ID,
			"profile-submit-"+suffix,
			guidance.GuidanceSubmission{
				SourceAssistantMessageID: source.ID,
				SourcePartID:             source.Parts[0].ID,
				Intent:                   guidance.IntentConfirmBrief,
				ProfileDecision:          decision,
			},
			"gpt-test",
			"high",
			"high",
		); err != nil {
			t.Fatalf("submit %s proposal: %v", suffix, err)
		}
	}
	profileValue := func() string {
		t.Helper()
		facts, err := dataStore.RestaurantProfile(ctx, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(facts) == 0 {
			return ""
		}
		if len(facts) != 1 || facts[0].Field != "primary_customers" {
			t.Fatalf("restaurant profile facts = %#v", facts)
		}
		return facts[0].Value
	}

	submitProposal(
		"current-only-empty",
		guidance.ProfileUpdateProposal{
			Field: "primary_customers", Operation: "set",
			ProposedValue: "附近家庭顾客", Reason: "跨任务稳定信息",
		},
		guidance.ProfileDecisionCurrentTaskOnly,
	)
	if value := profileValue(); value != "" {
		t.Fatalf("current-task-only proposal wrote profile value %q", value)
	}

	submitProposal(
		"ignore-empty",
		guidance.ProfileUpdateProposal{
			Field: "primary_customers", Operation: "set",
			ProposedValue: "附近家庭顾客", Reason: "跨任务稳定信息",
		},
		guidance.ProfileDecisionIgnore,
	)
	if value := profileValue(); value != "" {
		t.Fatalf("ignored proposal wrote profile value %q", value)
	}

	submitProposal(
		"set",
		guidance.ProfileUpdateProposal{
			Field: "primary_customers", Operation: "set",
			ProposedValue: "附近家庭顾客", Reason: "跨任务稳定信息",
		},
		guidance.ProfileDecisionSave,
	)
	if value := profileValue(); value != "附近家庭顾客" {
		t.Fatalf("set profile value = %q", value)
	}

	submitProposal(
		"current-only-replace",
		guidance.ProfileUpdateProposal{
			Field: "primary_customers", Operation: "replace",
			ProposedValue: "家庭聚餐和宴请顾客", Reason: "用户本次提供了新值",
		},
		guidance.ProfileDecisionCurrentTaskOnly,
	)
	if value := profileValue(); value != "附近家庭顾客" {
		t.Fatalf("current-task-only replacement changed profile to %q", value)
	}

	submitProposal(
		"replace",
		guidance.ProfileUpdateProposal{
			Field: "primary_customers", Operation: "replace",
			ProposedValue: "家庭聚餐和宴请顾客", Reason: "用户确认替换长期信息",
		},
		guidance.ProfileDecisionSave,
	)
	if value := profileValue(); value != "家庭聚餐和宴请顾客" {
		t.Fatalf("replaced profile value = %q", value)
	}

	submitProposal(
		"delete",
		guidance.ProfileUpdateProposal{
			Field: "primary_customers", Operation: "delete",
			Reason: "用户确认不再保留这条信息",
		},
		guidance.ProfileDecisionSave,
	)
	if value := profileValue(); value != "" {
		t.Fatalf("deleted profile value = %q", value)
	}
	var auditCount int
	if err := dataStore.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM restaurant_profile_audit WHERE user_id = ?
	`, user.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 3 {
		t.Fatalf("profile audit rows = %d, want 3 saved mutations", auditCount)
	}
}

func completeClarificationSource(
	t *testing.T,
	dataStore *Store,
	userID string,
	conversationID string,
) Message {
	t.Helper()
	_, assistant, err := dataStore.BeginResponse(
		context.Background(), userID, conversationID, "source-request",
		"帮我设计会员体系", "gpt-test", "high", "high", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	cards := guidance.ClarificationCards{
		SchemaVersion: guidance.SchemaVersion,
		InstanceID:    assistant.ID,
		Questions: []guidance.ClarificationQuestion{
			{
				Key: "goal", Prompt: "首要目标是什么？", Selection: "single_select",
				Options: []guidance.ClarificationOption{
					{Key: "repeat", Label: "增加复购"},
					{Key: "cash", Label: "回笼资金"},
				},
				AllowOther: true, AllowDelegatedDefault: true,
			},
			{
				Key: "audience", Prompt: "主要顾客是谁？", Selection: "single_select",
				Options: []guidance.ClarificationOption{
					{Key: "family", Label: "家庭聚餐"},
					{Key: "business", Label: "商务宴请"},
				},
				AllowOther: true, AllowDelegatedDefault: true,
			},
		},
	}
	raw, err := json.Marshal(cards)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := dataStore.CompleteAssistant(
		context.Background(), userID, assistant.ID, AssistantResult{
			Status: "completed",
			Parts: []NewMessagePart{{
				Type:        guidance.PartClarification,
				TextContent: guidance.NormalizeClarificationCards(cards),
				JSONContent: raw,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return completed
}

func completeTaskBriefSource(
	t *testing.T,
	dataStore *Store,
	userID string,
	conversationID string,
) Message {
	t.Helper()
	return completeTaskBriefProposalSource(
		t,
		dataStore,
		userID,
		conversationID,
		"brief-source-request",
		guidance.ProfileUpdateProposal{
			Field:         "primary_customers",
			Operation:     "set",
			ProposedValue: "附近家庭聚餐顾客",
			Reason:        "这是跨任务稳定信息",
		},
	)
}

func completeTaskBriefProposalSource(
	t *testing.T,
	dataStore *Store,
	userID string,
	conversationID string,
	requestID string,
	proposal guidance.ProfileUpdateProposal,
) Message {
	t.Helper()
	_, assistant, err := dataStore.BeginResponse(
		context.Background(), userID, conversationID, requestID,
		"帮我设计会员体系", "gpt-test", "high", "high", nil,
	)
	if err != nil {
		t.Fatalf("begin task brief source %s: %v", requestID, err)
	}
	brief := guidance.TaskBrief{
		SchemaVersion:         guidance.SchemaVersion,
		InstanceID:            assistant.ID,
		Goal:                  "设计餐厅会员体系",
		DesiredOutput:         []string{"充值档位与使用规则"},
		ProfileUpdateProposal: &proposal,
	}
	raw, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := dataStore.CompleteAssistant(
		context.Background(), userID, assistant.ID, AssistantResult{
			Status: "completed",
			Parts: []NewMessagePart{{
				Type:        guidance.PartTaskBrief,
				TextContent: guidance.NormalizeTaskBrief(brief),
				JSONContent: raw,
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return completed
}
