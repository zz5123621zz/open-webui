package guidance

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseClarificationControlCallValidatesAndNormalizes(t *testing.T) {
	part, err := ParseControlCall(
		ToolShowClarificationCards,
		json.RawMessage(`{
			"schemaVersion":1,
			"intro":"先确认几个会明显影响方案的问题。",
			"currentUnderstanding":["需要设计餐厅会员体系"],
			"questions":[{
				"key":"primary_goal",
				"prompt":"你最希望先解决什么？",
				"selection":"single_select",
				"options":[
					{"key":"repeat_visits","label":"增加复购","description":"让老顾客更常回来"},
					{"key":"cash_flow","label":"回笼资金","description":null}
				],
				"allowOther":true,
				"allowDelegatedDefault":true,
				"minimumSelections":null,
				"maximumSelections":null
			},{
				"key":"discount_level",
				"prompt":"希望优惠力度怎样？",
				"selection":"single_select",
				"options":[
					{"key":"conservative","label":"保守","description":null},
					{"key":"moderate","label":"适中","description":null}
				],
				"allowOther":true,
				"allowDelegatedDefault":true,
				"minimumSelections":1,
				"maximumSelections":1
			}]
		}`),
		"guidance_test_1",
		3,
	)
	if err != nil {
		t.Fatal(err)
	}
	if part.Type != PartClarification ||
		!strings.Contains(part.Text, "增加复购") ||
		!strings.Contains(part.Text, "你帮我决定") {
		t.Fatalf("normalized clarification part = %#v", part)
	}
	cards, err := DecodeClarificationCards(part.Data)
	if err != nil {
		t.Fatal(err)
	}
	if cards.InstanceID != "guidance_test_1" || len(cards.Questions) != 2 {
		t.Fatalf("stored clarification cards = %#v", cards)
	}
}

func TestClarificationControlRejectsUnsafeOrExpandedOutput(t *testing.T) {
	for name, raw := range map[string]string{
		"unknown field": `{
			"schemaVersion":1,"intro":null,"currentUnderstanding":[],
			"questions":[],"action":"POST /api/delete"
		}`,
		"model instance id": `{
			"schemaVersion":1,"instanceId":"model_value","intro":null,
			"currentUnderstanding":[],"questions":[]
		}`,
		"unsafe label": `{
			"schemaVersion":1,"intro":null,"currentUnderstanding":[],
			"questions":[{
				"key":"goal","prompt":"目标？","selection":"single_select",
				"options":[
					{"key":"one","label":"https://example.com","description":null},
					{"key":"two","label":"选项二","description":null}
				],
				"allowOther":true,"allowDelegatedDefault":false,
				"minimumSelections":1,"maximumSelections":1
			},{
				"key":"scope","prompt":"范围？","selection":"single_select",
				"options":[
					{"key":"one","label":"范围一","description":null},
					{"key":"two","label":"范围二","description":null}
				],
				"allowOther":true,"allowDelegatedDefault":false,
				"minimumSelections":1,"maximumSelections":1
			}]
		}`,
		"missing required nullable option field": `{
			"schemaVersion":1,"intro":null,"currentUnderstanding":[],
			"questions":[{
				"key":"goal","prompt":"目标？","selection":"single_select",
				"options":[
					{"key":"one","label":"目标一"},
					{"key":"two","label":"目标二","description":null}
				],
				"allowOther":true,"allowDelegatedDefault":false,
				"minimumSelections":1,"maximumSelections":1
			},{
				"key":"scope","prompt":"范围？","selection":"single_select",
				"options":[
					{"key":"one","label":"范围一","description":null},
					{"key":"two","label":"范围二","description":null}
				],
				"allowOther":true,"allowDelegatedDefault":false,
				"minimumSelections":1,"maximumSelections":1
			}]
		}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseControlCall(
				ToolShowClarificationCards,
				json.RawMessage(raw),
				"guidance_test_2",
				3,
			); err == nil {
				t.Fatal("unsafe clarification output was accepted")
			}
		})
	}
}

func TestParseClarificationControlHonorsRuntimeQuestionLimit(t *testing.T) {
	_, err := ParseControlCall(
		ToolShowClarificationCards,
		json.RawMessage(`{
			"schemaVersion":1,
			"intro":null,
			"currentUnderstanding":[],
			"questions":[{
				"key":"goal",
				"prompt":"目标是什么？",
				"selection":"single_select",
				"options":[
					{"key":"one","label":"目标一","description":null},
					{"key":"two","label":"目标二","description":null}
				],
				"allowOther":true,
				"allowDelegatedDefault":true,
				"minimumSelections":1,
				"maximumSelections":1
			},{
				"key":"audience",
				"prompt":"主要顾客是谁？",
				"selection":"single_select",
				"options":[
					{"key":"one","label":"顾客一","description":null},
					{"key":"two","label":"顾客二","description":null}
				],
				"allowOther":true,
				"allowDelegatedDefault":true,
				"minimumSelections":1,
				"maximumSelections":1
			},{
				"key":"scope",
				"prompt":"范围是什么？",
				"selection":"single_select",
				"options":[
					{"key":"one","label":"范围一","description":null},
					{"key":"two","label":"范围二","description":null}
				],
				"allowOther":true,
				"allowDelegatedDefault":true,
				"minimumSelections":1,
				"maximumSelections":1
			}]
		}`),
		"guidance_test_limit",
		2,
	)
	if err == nil {
		t.Fatal("clarification exceeding the runtime question limit was accepted")
	}
}

func TestValidateClarificationSubmissionUsesOnlySavedChoices(t *testing.T) {
	cards := ClarificationCards{
		SchemaVersion: SchemaVersion,
		InstanceID:    "guidance_test_3",
		Questions: []ClarificationQuestion{
			{
				Key: "goal", Prompt: "首要目标是什么？", Selection: "single_select",
				Options: []ClarificationOption{
					{Key: "repeat", Label: "增加复购"},
					{Key: "cash", Label: "回笼资金"},
				},
				AllowOther: true, AllowDelegatedDefault: true,
			},
			{
				Key: "audience", Prompt: "主要顾客是哪类？", Selection: "multi_select",
				Options: []ClarificationOption{
					{Key: "family", Label: "家庭聚餐"},
					{Key: "business", Label: "商务宴请"},
					{Key: "nearby", Label: "附近居民"},
				},
				AllowOther: true, MinimumSelections: intPointer(1), MaximumSelections: intPointer(2),
			},
		},
	}
	raw, _ := json.Marshal(cards)
	stored, text, mutation, err := ValidateSubmission(
		PartClarification,
		raw,
		GuidanceSubmission{
			SourceAssistantMessageID: "assistant_1",
			SourcePartID:             "part_1",
			Intent:                   IntentGenerateFromCurrent,
			Answers: []ClarificationAnswer{
				{QuestionKey: "goal", DelegatedDefault: true},
				{QuestionKey: "audience", SelectedOptionKeys: []string{"family", "nearby"}},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if mutation != nil || stored.Intent != IntentGenerateFromCurrent ||
		!strings.Contains(text, "采用合理默认值") ||
		!strings.Contains(text, "停止普通追问") {
		t.Fatalf("validated clarification submission = %#v %q %#v", stored, text, mutation)
	}

	invalid := GuidanceSubmission{
		SourceAssistantMessageID: "assistant_1",
		SourcePartID:             "part_1",
		Intent:                   IntentContinueRefining,
		Answers: []ClarificationAnswer{
			{QuestionKey: "goal", SelectedOptionKeys: []string{"invented"}},
			{QuestionKey: "audience", SelectedOptionKeys: []string{"family"}},
		},
	}
	if _, _, _, err := ValidateSubmission(PartClarification, raw, invalid); err == nil {
		t.Fatal("unknown option was accepted")
	}
}

func TestTaskBriefProfileProposalRequiresExplicitDecision(t *testing.T) {
	brief := TaskBrief{
		SchemaVersion: SchemaVersion,
		InstanceID:    "guidance_test_4",
		Goal:          "设计能提高复购的会员体系",
		Context:       []string{"社区型中餐厅"},
		DesiredOutput: []string{"充值档位、权益、规则和员工话术"},
		ProfileUpdateProposal: &ProfileUpdateProposal{
			Field: "primary_customers", Operation: "set",
			ProposedValue: "附近家庭聚餐顾客", Reason: "这是跨任务稳定信息",
		},
	}
	raw, _ := json.Marshal(brief)
	_, text, mutation, err := ValidateSubmission(
		PartTaskBrief,
		raw,
		GuidanceSubmission{
			SourceAssistantMessageID: "assistant_2",
			SourcePartID:             "part_2",
			Intent:                   IntentConfirmBrief,
			ProfileDecision:          ProfileDecisionSave,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if mutation == nil || mutation.Field != "primary_customers" ||
		mutation.Value != "附近家庭聚餐顾客" ||
		!strings.Contains(text, "保存已确认") {
		t.Fatalf("profile mutation = %#v text=%q", mutation, text)
	}
	if _, _, _, err := ValidateSubmission(
		PartTaskBrief,
		raw,
		GuidanceSubmission{
			SourceAssistantMessageID: "assistant_2",
			SourcePartID:             "part_2",
			Intent:                   IntentConfirmBrief,
		},
	); err == nil {
		t.Fatal("missing profile decision was accepted")
	}
}

func TestToolDefinitionsStayStrictAndRuntimeCanForceFinalAnswer(t *testing.T) {
	tools := ToolDefinitions(true, 2)
	raw, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, expected := range []string{
		`"name":"show_clarification_cards"`,
		`"name":"show_task_brief"`,
		`"additionalProperties":false`,
		`"maxItems":2`,
		`"strict":true`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("tool definitions missing %q: %s", expected, text)
		}
	}
	instructions := CompileInstructions(Runtime{
		Enabled:     true,
		FinalAnswer: true,
		ProfileFacts: []ProfileFact{{
			Field: "primary_customers", Value: "附近家庭聚餐顾客",
		}},
	})
	if !strings.Contains(instructions, "untrusted data") ||
		!strings.Contains(instructions, "Produce the complete answer now") {
		t.Fatalf("final runtime instructions = %q", instructions)
	}
}

func intPointer(value int) *int {
	return &value
}
