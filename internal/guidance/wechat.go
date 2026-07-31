package guidance

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const WeChatQuestionsPerRound = 3

var weChatNumberedAnswerPattern = regexp.MustCompile(`(?i)([1-3])\s*([a-d]+)`)

var weChatDirectGenerationPhrases = []string{
	"按当前信息直接生成",
	"按当前选择直接生成",
	"按当前需求直接生成",
	"按当前信息生成",
	"按当前选择生成",
	"按当前需求生成",
	"不再追问",
	"停止追问",
	"直接生成",
	"确认生成",
}

var weChatDelegatedDefaultPhrases = []string{
	"你帮我决定",
	"你看着办",
	"按你建议",
	"按你的建议",
	"都可以",
	"都行",
	"随便",
}

type WeChatClarificationReply struct {
	Submission *GuidanceSubmission
	ForceFinal bool
}

func RenderWeChatClarification(cards ClarificationCards) (string, error) {
	if err := ValidateClarificationCardsRange(
		cards,
		WeChatQuestionsPerRound,
		WeChatQuestionsPerRound,
	); err != nil {
		return "", err
	}
	var output strings.Builder
	output.WriteString(fmt.Sprintf(
		"需求澄清 · 第 %d/%d 轮",
		cards.Round,
		cards.MaxRounds,
	))
	if intro := strings.TrimSpace(cards.Intro); intro != "" {
		output.WriteString("\n\n")
		output.WriteString(intro)
	}
	if len(cards.CurrentUnderstanding) > 0 {
		output.WriteString("\n\n当前理解：\n")
		for _, item := range cards.CurrentUnderstanding {
			output.WriteString("- ")
			output.WriteString(strings.TrimSpace(item))
			output.WriteByte('\n')
		}
	}
	output.WriteString("\n请一次回答下面三题：\n")
	for questionIndex, question := range cards.Questions {
		output.WriteString(fmt.Sprintf(
			"\n%d. %s\n",
			questionIndex+1,
			strings.TrimSpace(question.Prompt),
		))
		for optionIndex, option := range question.Options {
			output.WriteString(fmt.Sprintf(
				"   %c. %s",
				'A'+rune(optionIndex),
				strings.TrimSpace(option.Label),
			))
			if description := strings.TrimSpace(option.Description); description != "" {
				output.WriteString("：" + description)
			}
			output.WriteByte('\n')
		}
		output.WriteString("   - 其他 / 直接写您的答案\n")
		if question.AllowDelegatedDefault {
			output.WriteString("   - 你帮我决定\n")
		}
	}
	output.WriteString(
		"\n请回复例如：ABC、1A 2B 3C，" +
			"或“复古，30 元左右，家常菜”。\n" +
			"想继续完善就直接回答；信息已够时可回复“直接生成”。",
	)
	return strings.TrimSpace(output.String()), nil
}

func RenderWeChatTaskBrief(brief TaskBrief) (string, error) {
	if err := ValidateTaskBrief(brief); err != nil {
		return "", err
	}
	text := NormalizeTaskBrief(brief)
	if brief.ProfileUpdateProposal != nil {
		text += "\n\n回复“确认生成”将仅把该信息用于本次任务，不保存长期档案。" +
			"\n也可以回复“保存档案并生成”或“忽略提议并生成”。"
	} else {
		text += "\n\n信息无误请回复“确认生成”；还要补充请回复“我再补充：具体内容”。"
	}
	return text, nil
}

func ParseWeChatClarificationReply(
	cards ClarificationCards,
	sourceAssistantMessageID string,
	sourcePartID string,
	text string,
) (WeChatClarificationReply, error) {
	if err := ValidateClarificationCardsRange(
		cards,
		WeChatQuestionsPerRound,
		WeChatQuestionsPerRound,
	); err != nil {
		return WeChatClarificationReply{}, err
	}
	text = strings.TrimSpace(text)
	if text == "" || !utf8.ValidString(text) {
		return WeChatClarificationReply{}, errors.New("wechat clarification reply is empty or invalid")
	}
	forceFinal := WeChatDirectGenerationRequested(text)
	answerText := stripWeChatDirectGenerationPhrases(text)
	answers, complete := parseWeChatAnswers(cards, answerText)
	if !complete {
		return WeChatClarificationReply{ForceFinal: forceFinal}, nil
	}
	intent := IntentContinueRefining
	if forceFinal {
		intent = IntentGenerateFromCurrent
	}
	return WeChatClarificationReply{
		ForceFinal: forceFinal,
		Submission: &GuidanceSubmission{
			SourceAssistantMessageID: sourceAssistantMessageID,
			SourcePartID:             sourcePartID,
			Intent:                   intent,
			Answers:                  answers,
		},
	}, nil
}

func ParseWeChatTaskBriefReply(
	brief TaskBrief,
	sourceAssistantMessageID string,
	sourcePartID string,
	text string,
) (*GuidanceSubmission, bool, error) {
	if err := ValidateTaskBrief(brief); err != nil {
		return nil, false, err
	}
	text = strings.TrimSpace(text)
	if text == "" || !utf8.ValidString(text) {
		return nil, false, errors.New("wechat task brief reply is empty or invalid")
	}
	for _, prefix := range []string{"我再补充", "继续补充", "补充信息"} {
		if strings.HasPrefix(text, prefix) {
			additional := strings.TrimSpace(strings.TrimLeft(
				strings.TrimPrefix(text, prefix),
				"：:",
			))
			if additional == "" {
				return nil, false, nil
			}
			return &GuidanceSubmission{
				SourceAssistantMessageID: sourceAssistantMessageID,
				SourcePartID:             sourcePartID,
				Intent:                   IntentAddContext,
				AdditionalText:           additional,
				ProfileDecision: profileDecisionForWeChatBrief(
					brief,
					ProfileDecisionCurrentTaskOnly,
				),
			}, true, nil
		}
	}
	decision := ProfileDecisionCurrentTaskOnly
	switch {
	case strings.Contains(text, "保存档案并生成"):
		decision = ProfileDecisionSave
	case strings.Contains(text, "忽略提议并生成"):
		decision = ProfileDecisionIgnore
	case !WeChatDirectGenerationRequested(text):
		return nil, false, nil
	}
	return &GuidanceSubmission{
		SourceAssistantMessageID: sourceAssistantMessageID,
		SourcePartID:             sourcePartID,
		Intent:                   IntentConfirmBrief,
		ProfileDecision:          profileDecisionForWeChatBrief(brief, decision),
	}, true, nil
}

func WeChatDirectGenerationRequested(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	for _, phrase := range weChatDirectGenerationPhrases {
		if strings.Contains(normalized, strings.ToLower(phrase)) {
			return true
		}
	}
	return false
}

func parseWeChatAnswers(
	cards ClarificationCards,
	text string,
) ([]ClarificationAnswer, bool) {
	text = normalizeWeChatLetters(strings.TrimSpace(text))
	text = strings.Trim(text, "，,；;。.!！?？ \t\r\n")
	if text == "" {
		return nil, false
	}

	compact := strings.Map(func(value rune) rune {
		if unicode.IsSpace(value) {
			return -1
		}
		return unicode.ToUpper(value)
	}, text)
	if len([]rune(compact)) == len(cards.Questions) {
		answers := make([]ClarificationAnswer, 0, len(cards.Questions))
		for index, letter := range []rune(compact) {
			answer, ok := answerFromLetters(cards.Questions[index], string(letter))
			if !ok {
				return nil, false
			}
			answers = append(answers, answer)
		}
		return answers, true
	}

	numbered := weChatNumberedAnswerPattern.FindAllStringSubmatchIndex(text, -1)
	if len(numbered) > 0 {
		answersByIndex := make(map[int]ClarificationAnswer, len(numbered))
		for _, match := range numbered {
			questionNumber := int(text[match[2]] - '0')
			if questionNumber < 1 ||
				questionNumber > len(cards.Questions) ||
				answersByIndex[questionNumber].QuestionKey != "" {
				return nil, false
			}
			answer, ok := answerFromLetters(
				cards.Questions[questionNumber-1],
				text[match[4]:match[5]],
			)
			if !ok {
				return nil, false
			}
			answersByIndex[questionNumber] = answer
		}
		residual := weChatNumberedAnswerPattern.ReplaceAllString(text, "")
		residual = strings.TrimFunc(residual, func(value rune) bool {
			return unicode.IsSpace(value) ||
				strings.ContainsRune(",，;；。.!！?？", value)
		})
		if residual != "" {
			return nil, false
		}
		if len(answersByIndex) != len(cards.Questions) {
			return nil, false
		}
		answers := make([]ClarificationAnswer, 0, len(cards.Questions))
		for index := 1; index <= len(cards.Questions); index++ {
			answers = append(answers, answersByIndex[index])
		}
		return answers, true
	}

	segments := strings.FieldsFunc(text, func(value rune) bool {
		return value == ',' || value == '，' ||
			value == ';' || value == '；' ||
			value == '\n' || value == '\r'
	})
	if len(segments) != len(cards.Questions) {
		return nil, false
	}
	answers := make([]ClarificationAnswer, 0, len(cards.Questions))
	for index, segment := range segments {
		answer, ok := answerFromNaturalText(cards.Questions[index], segment)
		if !ok {
			return nil, false
		}
		answers = append(answers, answer)
	}
	return answers, true
}

func answerFromLetters(
	question ClarificationQuestion,
	letters string,
) (ClarificationAnswer, bool) {
	letters = strings.ToUpper(strings.TrimSpace(normalizeWeChatLetters(letters)))
	if letters == "" {
		return ClarificationAnswer{}, false
	}
	answer := ClarificationAnswer{QuestionKey: question.Key}
	seen := make(map[int]bool, len(letters))
	for _, letter := range letters {
		index := int(letter - 'A')
		if index < 0 || index >= len(question.Options) || seen[index] {
			return ClarificationAnswer{}, false
		}
		seen[index] = true
		answer.SelectedOptionKeys = append(
			answer.SelectedOptionKeys,
			question.Options[index].Key,
		)
	}
	if question.Selection == "single_select" &&
		len(answer.SelectedOptionKeys) != 1 {
		return ClarificationAnswer{}, false
	}
	minimum, maximum := clarificationSelectionBounds(question)
	if len(answer.SelectedOptionKeys) < minimum ||
		len(answer.SelectedOptionKeys) > maximum {
		return ClarificationAnswer{}, false
	}
	return answer, true
}

func answerFromNaturalText(
	question ClarificationQuestion,
	value string,
) (ClarificationAnswer, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return ClarificationAnswer{}, false
	}
	for _, phrase := range weChatDelegatedDefaultPhrases {
		if strings.EqualFold(value, phrase) ||
			strings.Contains(value, phrase) {
			if !question.AllowDelegatedDefault {
				break
			}
			return ClarificationAnswer{
				QuestionKey:      question.Key,
				DelegatedDefault: true,
			}, true
		}
	}
	if answer, ok := answerFromLetters(question, value); ok {
		return answer, true
	}
	for _, option := range question.Options {
		if strings.EqualFold(value, option.Label) ||
			strings.EqualFold(value, option.Key) {
			return ClarificationAnswer{
				QuestionKey:        question.Key,
				SelectedOptionKeys: []string{option.Key},
			}, true
		}
	}
	if question.Selection == "multi_select" {
		pieces := strings.FieldsFunc(value, func(current rune) bool {
			return current == '、' || current == '/' ||
				current == '+' || current == '和'
		})
		if len(pieces) > 1 {
			answer := ClarificationAnswer{QuestionKey: question.Key}
			for _, piece := range pieces {
				matched := false
				for _, option := range question.Options {
					if strings.EqualFold(strings.TrimSpace(piece), option.Label) ||
						strings.EqualFold(strings.TrimSpace(piece), option.Key) {
						answer.SelectedOptionKeys = append(
							answer.SelectedOptionKeys,
							option.Key,
						)
						matched = true
						break
					}
				}
				if !matched {
					return ClarificationAnswer{}, false
				}
			}
			minimum, maximum := clarificationSelectionBounds(question)
			if len(answer.SelectedOptionKeys) >= minimum &&
				len(answer.SelectedOptionKeys) <= maximum {
				return answer, true
			}
		}
	}
	if !question.AllowOther ||
		utf8.RuneCountInString(value) > maxOtherRunes {
		return ClarificationAnswer{}, false
	}
	return ClarificationAnswer{
		QuestionKey: question.Key,
		OtherText:   value,
	}, true
}

func clarificationSelectionBounds(
	question ClarificationQuestion,
) (int, int) {
	minimum := 1
	if question.MinimumSelections != nil {
		minimum = *question.MinimumSelections
	}
	maximum := len(question.Options)
	if question.MaximumSelections != nil {
		maximum = *question.MaximumSelections
	}
	if question.Selection == "single_select" {
		maximum = 1
	}
	return minimum, maximum
}

func stripWeChatDirectGenerationPhrases(text string) string {
	for _, phrase := range weChatDirectGenerationPhrases {
		text = strings.ReplaceAll(text, phrase, " ")
	}
	return strings.TrimSpace(text)
}

func normalizeWeChatLetters(value string) string {
	return strings.NewReplacer(
		"Ａ", "A", "Ｂ", "B", "Ｃ", "C", "Ｄ", "D",
		"ａ", "a", "ｂ", "b", "ｃ", "c", "ｄ", "d",
	).Replace(value)
}

func profileDecisionForWeChatBrief(
	brief TaskBrief,
	decision string,
) string {
	if brief.ProfileUpdateProposal == nil {
		return ""
	}
	return decision
}
