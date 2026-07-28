package httpapi

import (
	"context"
	"fmt"

	"github.com/owui-personal-slim/owui-personal-slim/internal/guidance"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

func (s *Server) guidanceRuntime(
	ctx context.Context,
	userID string,
	messages []store.Message,
	forceFinal bool,
	generateImage bool,
) (guidance.Runtime, error) {
	if !s.cfg.Tools.RestaurantGuidanceEnabled || generateImage {
		return guidance.Runtime{}, nil
	}
	workbench, err := s.store.WorkbenchSetting(ctx, userID)
	if err != nil {
		return guidance.Runtime{}, err
	}
	if workbench.Initial != guidance.WorkbenchRestaurant ||
		workbench.Effective != guidance.WorkbenchRestaurant {
		return guidance.Runtime{}, nil
	}
	profile, err := s.store.RestaurantProfile(ctx, userID)
	if err != nil {
		return guidance.Runtime{}, err
	}
	runtime := guidance.Runtime{
		Enabled:        true,
		AllowTaskBrief: true,
		MaxRounds:      guidance.MaximumClarificationRounds,
	}
	for _, fact := range profile {
		runtime.ProfileFacts = append(runtime.ProfileFacts, guidance.ProfileFact{
			Field: fact.Field,
			Value: fact.Value,
		})
	}

	taskStart := -1
	for index, message := range messages {
		if message.Role != "user" || hasGuidanceSubmission(message) {
			continue
		}
		if index > 0 && hasInteractiveGuidancePart(messages[index-1]) {
			continue
		}
		taskStart = index
	}
	if taskStart < 0 {
		runtime.AllowClarification = true
		runtime.MaxQuestions = guidance.MaximumQuestionsPerRound
		runtime.FinalAnswer = forceFinal
		if forceFinal {
			runtime.AllowClarification = false
			runtime.AllowTaskBrief = false
		}
		return runtime, nil
	}

	var latestIntent string
	latestDelegatedDefault := false
	addContextCount := 0
	for index, message := range messages[taskStart:] {
		if index > 0 &&
			message.Role == "user" &&
			!hasGuidanceSubmission(message) {
			latestDelegatedDefault = false
			previous := messages[taskStart+index-1]
			switch {
			case hasGuidancePart(previous, guidance.PartClarification):
				latestIntent = guidance.IntentContinueRefining
			case hasGuidancePart(previous, guidance.PartTaskBrief):
				latestIntent = guidance.IntentAddContext
				addContextCount++
			}
		}
		for _, part := range message.Parts {
			switch part.Type {
			case guidance.PartClarification:
				cards, err := guidance.DecodeClarificationCards(part.JSONContent)
				if err != nil {
					return guidance.Runtime{}, fmt.Errorf("decode stored clarification: %w", err)
				}
				runtime.RoundCount++
				runtime.QuestionCount += len(cards.Questions)
			case guidance.PartClarificationSubmission:
				submission, err := guidance.DecodeStoredSubmission(part.JSONContent)
				if err != nil {
					return guidance.Runtime{}, fmt.Errorf("decode stored guidance submission: %w", err)
				}
				latestIntent = submission.Intent
				latestDelegatedDefault = false
				for _, answer := range submission.Answers {
					if answer.DelegatedDefault {
						latestDelegatedDefault = true
						break
					}
				}
				if submission.Intent == guidance.IntentAddContext {
					addContextCount++
				}
			}
		}
	}
	runtime.UserRequestedExtra = addContextCount > 0
	runtime.FinalAnswer = forceFinal ||
		latestIntent == guidance.IntentGenerateFromCurrent ||
		latestIntent == guidance.IntentConfirmBrief
	if runtime.FinalAnswer {
		runtime.AllowTaskBrief = false
		return runtime, nil
	}

	requireTaskBrief := func() (guidance.Runtime, error) {
		runtime.AllowClarification = false
		runtime.AllowTaskBrief = true
		runtime.RequireClarification = false
		runtime.RequireTaskBrief = true
		runtime.MaxQuestions = 0
		return runtime, nil
	}
	if latestDelegatedDefault {
		return requireTaskBrief()
	}

	if latestIntent == guidance.IntentContinueRefining {
		if runtime.RoundCount >= runtime.MaxRounds {
			return requireTaskBrief()
		}
		runtime.AllowClarification = true
		runtime.AllowTaskBrief = false
		runtime.RequireClarification = true
		runtime.MaxQuestions = guidance.MaximumQuestionsPerRound
		return runtime, nil
	}
	if latestIntent == guidance.IntentAddContext && addContextCount == 1 {
		if runtime.RoundCount >= runtime.MaxRounds {
			return requireTaskBrief()
		}
		runtime.AllowClarification = true
		runtime.MaxQuestions = guidance.MaximumQuestionsPerRound
		return runtime, nil
	}
	if runtime.RoundCount >= runtime.MaxRounds {
		return requireTaskBrief()
	}
	runtime.AllowClarification = true
	runtime.MaxQuestions = guidance.MaximumQuestionsPerRound
	return runtime, nil
}

func hasInteractiveGuidancePart(message store.Message) bool {
	return hasGuidancePart(message, guidance.PartClarification) ||
		hasGuidancePart(message, guidance.PartTaskBrief) ||
		hasGuidancePart(message, guidance.PartGuidanceError)
}

func hasGuidancePart(message store.Message, partType string) bool {
	for _, part := range message.Parts {
		if part.Type == partType {
			return true
		}
	}
	return false
}

func hasGuidanceSubmission(message store.Message) bool {
	for _, part := range message.Parts {
		if part.Type == guidance.PartClarificationSubmission {
			return true
		}
	}
	return false
}
