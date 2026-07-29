package guidance

import (
	"strings"
	"testing"
)

func TestWeChatClarificationRequiresExactlyThreeQuestions(t *testing.T) {
	cards := testWeChatCards()
	if _, err := RenderWeChatClarification(cards); err != nil {
		t.Fatalf("RenderWeChatClarification(3 questions) error = %v", err)
	}
	for _, count := range []int{2, 4} {
		invalid := cards
		if count == 2 {
			invalid.Questions = invalid.Questions[:2]
		} else {
			invalid.Questions = append(
				append([]ClarificationQuestion{}, invalid.Questions...),
				invalid.Questions[0],
			)
			invalid.Questions[3].Key = "occasion_extra"
		}
		if _, err := RenderWeChatClarification(invalid); err == nil {
			t.Fatalf("RenderWeChatClarification(%d questions) succeeded", count)
		}
	}
}

func TestRenderWeChatClarificationContainsTextProtocol(t *testing.T) {
	rendered, err := RenderWeChatClarification(testWeChatCards())
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"需求澄清 · 第 1/3 轮",
		"1. 菜品风格？",
		"A. 中式复古",
		"2. 可接受的价位？",
		"3. 主要场景？",
		"ABC",
		"1A 2B 3C",
		"复古，30 元左右，家常菜",
		"直接生成",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("rendered clarification lacks %q:\n%s", expected, rendered)
		}
	}
}

func TestParseWeChatClarificationLetterForms(t *testing.T) {
	cards := testWeChatCards()
	tests := []struct {
		name     string
		input    string
		expected [][]string
	}{
		{
			name:  "compact uppercase",
			input: "ABC",
			expected: [][]string{
				{"retro"}, {"mid"}, {"family"},
			},
		},
		{
			name:  "compact lowercase and spaces",
			input: "a b c",
			expected: [][]string{
				{"retro"}, {"mid"}, {"family"},
			},
		},
		{
			name:  "numbered multi select",
			input: "1A 2AC 3C",
			expected: [][]string{
				{"retro"}, {"budget", "premium"}, {"family"},
			},
		},
		{
			name:  "full width letters",
			input: "1Ａ 2Ｂ 3Ｃ",
			expected: [][]string{
				{"retro"}, {"mid"}, {"family"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reply, err := ParseWeChatClarificationReply(
				cards,
				"assistant-id",
				"part-id",
				test.input,
			)
			if err != nil {
				t.Fatal(err)
			}
			if reply.Submission == nil {
				t.Fatalf("submission is nil for %q", test.input)
			}
			if reply.Submission.Intent != IntentContinueRefining {
				t.Fatalf("intent = %q", reply.Submission.Intent)
			}
			if len(reply.Submission.Answers) != 3 {
				t.Fatalf("answers = %#v", reply.Submission.Answers)
			}
			for index, answer := range reply.Submission.Answers {
				if strings.Join(answer.SelectedOptionKeys, ",") !=
					strings.Join(test.expected[index], ",") {
					t.Errorf(
						"answer %d = %#v, want %#v",
						index,
						answer.SelectedOptionKeys,
						test.expected[index],
					)
				}
			}
		})
	}
}

func TestParseWeChatClarificationNaturalTextSeparators(t *testing.T) {
	cards := testWeChatCards()
	for name, input := range map[string]string{
		"Chinese comma": "中式复古，30 元左右，家庭聚餐",
		"ASCII comma":   "中式复古,30 元左右,家庭聚餐",
		"semicolon":     "中式复古;30 元左右;家庭聚餐",
		"newlines":      "中式复古\n30 元左右\n家庭聚餐",
	} {
		t.Run(name, func(t *testing.T) {
			reply, err := ParseWeChatClarificationReply(
				cards,
				"assistant-id",
				"part-id",
				input,
			)
			if err != nil {
				t.Fatal(err)
			}
			if reply.Submission == nil ||
				len(reply.Submission.Answers) != 3 {
				t.Fatalf("reply = %#v", reply)
			}
			got := reply.Submission.Answers
			if got[0].SelectedOptionKeys[0] != "retro" ||
				got[1].OtherText != "30 元左右" ||
				got[2].SelectedOptionKeys[0] != "family" {
				t.Fatalf("answers = %#v", got)
			}
		})
	}
}

func TestParseWeChatClarificationDoesNotInventInvalidAnswers(t *testing.T) {
	cards := testWeChatCards()
	for _, input := range []string{
		"AB",
		"ABE",
		"1A 2B",
		"1A 1B 2A 3A",
		"1A 2ABC 3A",
		"A,B",
	} {
		reply, err := ParseWeChatClarificationReply(
			cards,
			"assistant-id",
			"part-id",
			input,
		)
		if err != nil {
			t.Fatalf("%q: %v", input, err)
		}
		if reply.Submission != nil {
			t.Errorf("%q produced submission %#v", input, reply.Submission)
		}
	}
}

func TestParseWeChatDirectGeneration(t *testing.T) {
	cards := testWeChatCards()
	reply, err := ParseWeChatClarificationReply(
		cards,
		"assistant-id",
		"part-id",
		"直接生成",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reply.ForceFinal || reply.Submission != nil {
		t.Fatalf("direct reply = %#v", reply)
	}
	reply, err = ParseWeChatClarificationReply(
		cards,
		"assistant-id",
		"part-id",
		"ABC，按当前信息直接生成",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reply.ForceFinal ||
		reply.Submission == nil ||
		reply.Submission.Intent != IntentGenerateFromCurrent {
		t.Fatalf("combined direct reply = %#v", reply)
	}
}

func TestParseWeChatTaskBriefRepliesKeepProfileChoiceExplicit(t *testing.T) {
	brief := testWeChatBrief(true)
	tests := []struct {
		input    string
		intent   string
		decision string
		add      string
	}{
		{"确认生成", IntentConfirmBrief, ProfileDecisionCurrentTaskOnly, ""},
		{"保存档案并生成", IntentConfirmBrief, ProfileDecisionSave, ""},
		{"忽略提议并生成", IntentConfirmBrief, ProfileDecisionIgnore, ""},
		{"我再补充：预算上限 600 元", IntentAddContext, ProfileDecisionCurrentTaskOnly, "预算上限 600 元"},
	}
	for _, test := range tests {
		submission, matched, err := ParseWeChatTaskBriefReply(
			brief,
			"assistant-id",
			"part-id",
			test.input,
		)
		if err != nil {
			t.Fatalf("%q: %v", test.input, err)
		}
		if !matched || submission == nil {
			t.Fatalf("%q was not matched", test.input)
		}
		if submission.Intent != test.intent ||
			submission.ProfileDecision != test.decision ||
			submission.AdditionalText != test.add {
			t.Errorf("%q submission = %#v", test.input, submission)
		}
	}

	withoutProposal := testWeChatBrief(false)
	submission, matched, err := ParseWeChatTaskBriefReply(
		withoutProposal,
		"assistant-id",
		"part-id",
		"确认生成",
	)
	if err != nil || !matched || submission == nil {
		t.Fatalf("brief without proposal = %#v, %v, %v", submission, matched, err)
	}
	if submission.ProfileDecision != "" {
		t.Fatalf("profile decision without proposal = %q", submission.ProfileDecision)
	}
}

func TestWeChatExactQuestionToolSchemaAndInstructions(t *testing.T) {
	runtime := Runtime{
		Enabled:            true,
		AllowClarification: true,
		AllowTaskBrief:     true,
		MinQuestions:       3,
		MaxQuestions:       3,
		MaxRounds:          3,
	}
	tools := ToolDefinitions(runtime)
	if len(tools) != 2 {
		t.Fatalf("tools = %#v", tools)
	}
	parameters := tools[0]["parameters"].(map[string]any)
	properties := parameters["properties"].(map[string]any)
	questions := properties["questions"].(map[string]any)
	if questions["minItems"] != 3 || questions["maxItems"] != 3 {
		t.Fatalf("question schema = %#v", questions)
	}
	instructions := CompileInstructions(runtime)
	if !strings.Contains(instructions, "exactly 3 high-impact questions") {
		t.Fatalf("instructions do not require exactly three:\n%s", instructions)
	}
}

func testWeChatCards() ClarificationCards {
	one := 1
	two := 2
	return ClarificationCards{
		SchemaVersion:        SchemaVersion,
		InstanceID:           "wechat-cards",
		Round:                1,
		MaxRounds:            3,
		Intro:                "请补充关键信息。",
		CurrentUnderstanding: []string{"需要设计 20 道菜"},
		Questions: []ClarificationQuestion{
			{
				Key:       "style",
				Prompt:    "菜品风格？",
				Selection: "single_select",
				Options: []ClarificationOption{
					{Key: "retro", Label: "中式复古"},
					{Key: "western", Label: "西餐"},
					{Key: "homestyle", Label: "家常菜"},
					{Key: "fusion", Label: "融合菜"},
				},
				AllowOther: true, AllowDelegatedDefault: true,
				MinimumSelections: &one, MaximumSelections: &one,
			},
			{
				Key:       "price",
				Prompt:    "可接受的价位？",
				Selection: "multi_select",
				Options: []ClarificationOption{
					{Key: "budget", Label: "20 元以内"},
					{Key: "mid", Label: "20～30 元"},
					{Key: "premium", Label: "30～50 元"},
					{Key: "flexible", Label: "价格灵活"},
				},
				AllowOther: true, AllowDelegatedDefault: true,
				MinimumSelections: &one, MaximumSelections: &two,
			},
			{
				Key:       "occasion",
				Prompt:    "主要场景？",
				Selection: "single_select",
				Options: []ClarificationOption{
					{Key: "daily", Label: "日常散客"},
					{Key: "business", Label: "商务宴请"},
					{Key: "family", Label: "家庭聚餐"},
					{Key: "takeout", Label: "外卖"},
				},
				AllowOther: true, AllowDelegatedDefault: true,
				MinimumSelections: &one, MaximumSelections: &one,
			},
		},
	}
}

func testWeChatBrief(withProposal bool) TaskBrief {
	brief := TaskBrief{
		SchemaVersion:        SchemaVersion,
		InstanceID:           "wechat-brief",
		Goal:                 "设计 20 道菜品",
		Context:              []string{"中式复古"},
		Constraints:          []string{"单道 30 元左右"},
		DesiredOutput:        []string{"完整菜品清单"},
		DelegatedAssumptions: []string{},
		Unresolved:           []string{},
	}
	if withProposal {
		brief.ProfileUpdateProposal = &ProfileUpdateProposal{
			Field:         "cuisine_positioning",
			Operation:     "set",
			ProposedValue: "中式复古",
			Reason:        "可用于后续菜单设计",
		}
	}
	return brief
}
