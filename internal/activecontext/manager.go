package activecontext

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/owui-personal-slim/owui-personal-slim/internal/provider"
	"github.com/owui-personal-slim/owui-personal-slim/internal/store"
)

var ErrContextTooLarge = errors.New("conversation exceeds safe context window")

const (
	triggerPercent             = 80
	hardPercent                = 90
	targetPercent              = 60
	softRequestBytes     int64 = 45 * 1024 * 1024
	hardRequestBytes     int64 = 50 * 1024 * 1024
	targetRequestBytes   int64 = 30 * 1024 * 1024
	compactionBatchBytes       = 8 * 1024 * 1024
)

var checkpointSections = []string{
	"## 长期用户偏好",
	"## 事实与实体",
	"## 已作决定与约束",
	"## 当前话题状态",
	"## 未决问题与下一步",
	"## 重要附件、工具与引用",
}

type Manager struct {
	store            *store.Store
	provider         *provider.Client
	safetyIdentifier func(string) string
}

type Result struct {
	Checkpoint          *store.ContextCheckpoint
	Messages            []store.Message
	EstimatedTokens     int
	EstimatedBytes      int64
	CompactionAttempted bool
	CompactionWarning   error
}

type StatusFunc func(status string, data map[string]any) error

func New(
	dataStore *store.Store,
	providerClient *provider.Client,
	safetyIdentifier ...func(string) string,
) *Manager {
	manager := &Manager{store: dataStore, provider: providerClient}
	if len(safetyIdentifier) > 0 {
		manager.safetyIdentifier = safetyIdentifier[0]
	}
	return manager
}

func (m *Manager) Prepare(
	ctx context.Context,
	userID string,
	conversation store.Conversation,
	model provider.Model,
	sentEffort string,
	allMessages []store.Message,
	expectedHeadMessageID string,
	status StatusFunc,
) (Result, error) {
	return m.prepare(ctx, userID, conversation, model, sentEffort, allMessages, expectedHeadMessageID, false, status)
}

func (m *Manager) ForcePrepare(
	ctx context.Context,
	userID string,
	conversation store.Conversation,
	model provider.Model,
	sentEffort string,
	allMessages []store.Message,
	expectedHeadMessageID string,
	status StatusFunc,
) (Result, error) {
	return m.prepare(ctx, userID, conversation, model, sentEffort, allMessages, expectedHeadMessageID, true, status)
}

func (m *Manager) prepare(
	ctx context.Context,
	userID string,
	conversation store.Conversation,
	model provider.Model,
	sentEffort string,
	allMessages []store.Message,
	expectedHeadMessageID string,
	force bool,
	status StatusFunc,
) (Result, error) {
	contextMessages := replayableMessages(allMessages)
	activeMessages := contextMessages
	var previous *store.ContextCheckpoint
	if checkpoint, err := m.store.LatestCheckpoint(ctx, userID, conversation.ID); err == nil {
		previous = &checkpoint
		activeMessages = messagesAfter(contextMessages, checkpoint.BoundaryMessageID)
	} else if !errors.Is(err, store.ErrNotFound) {
		return Result{}, err
	}

	estimatedTokens := EstimateCheckpoint(previous) + EstimateMessages(activeMessages)
	estimatedBytes, err := m.estimateProviderBytes(ctx, userID, previous, activeMessages)
	if err != nil {
		return Result{}, err
	}
	triggerTokens := model.ContextWindow * triggerPercent / 100
	if !force && estimatedTokens < triggerTokens && estimatedBytes < softRequestBytes {
		return Result{
			Checkpoint: previous, Messages: activeMessages,
			EstimatedTokens: estimatedTokens, EstimatedBytes: estimatedBytes,
		}, nil
	}

	hardExceeded := estimatedTokens >= model.ContextWindow*hardPercent/100 ||
		estimatedBytes >= hardRequestBytes
	if status != nil {
		if err := status("started", map[string]any{
			"estimatedTokens": estimatedTokens,
			"estimatedBytes":  estimatedBytes,
			"contextWindow":   model.ContextWindow,
			"triggerPercent":  triggerPercent,
			"targetPercent":   targetPercent,
			"forced":          force,
		}); err != nil {
			return Result{}, err
		}
	}

	recentStart, err := m.chooseRecentStart(ctx, userID, activeMessages, model.ContextWindow*targetPercent/100)
	if err != nil || recentStart <= 0 || recentStart >= len(activeMessages) {
		if status != nil {
			_ = status("failed", map[string]any{"continuing": false})
		}
		return Result{}, ErrContextTooLarge
	}
	sourceMessages := activeMessages[:recentStart]
	recentMessages := activeMessages[recentStart:]

	summary, inputTokens, outputTokens, sourceBytes, err := m.completeCheckpoint(
		ctx, userID, conversation.Model, sentEffort, previous, sourceMessages, recentMessages,
	)
	if err != nil {
		if status != nil {
			_ = status("failed", map[string]any{"continuing": !hardExceeded})
		}
		if hardExceeded {
			return Result{}, fmt.Errorf("%w: compaction failed above hard threshold: %v", ErrContextTooLarge, err)
		}
		return Result{
			Checkpoint: previous, Messages: activeMessages,
			EstimatedTokens: estimatedTokens, EstimatedBytes: estimatedBytes,
			CompactionAttempted: true, CompactionWarning: err,
		}, nil
	}

	checkpointTokens := EstimateText(summary)
	afterTokens := checkpointTokens + EstimateMessages(recentMessages)
	checkpoint := store.ContextCheckpoint{
		ConversationID:        conversation.ID,
		Model:                 conversation.Model,
		SummaryText:           summary,
		BoundaryMessageID:     sourceMessages[len(sourceMessages)-1].ID,
		SourceFirstMessageID:  sourceMessages[0].ID,
		SourceLastMessageID:   sourceMessages[len(sourceMessages)-1].ID,
		EstimatedTokensBefore: estimatedTokens,
		EstimatedTokensAfter:  afterTokens,
		SourceBytes:           sourceBytes,
		InputTokens:           inputTokens,
		OutputTokens:          outputTokens,
		Status:                "completed",
		ExpectedHeadMessageID: expectedHeadMessageID,
	}
	if previous != nil {
		checkpoint.PreviousCheckpointID = previous.ID
	}
	afterBytes, err := m.estimateProviderBytes(ctx, userID, &checkpoint, recentMessages)
	if err != nil {
		return Result{}, err
	}
	if afterTokens >= model.ContextWindow*hardPercent/100 || afterBytes >= hardRequestBytes {
		if status != nil {
			_ = status("failed", map[string]any{"continuing": false})
		}
		return Result{}, ErrContextTooLarge
	}
	saved, err := m.store.CreateCheckpoint(ctx, userID, checkpoint)
	if err != nil {
		return Result{}, err
	}
	if status != nil {
		if err := status("completed", map[string]any{
			"checkpointId": saved.ID, "boundaryMessageId": saved.BoundaryMessageID,
			"estimatedTokensBefore": estimatedTokens, "estimatedTokensAfter": afterTokens,
			"estimatedBytesBefore": estimatedBytes, "estimatedBytesAfter": afterBytes,
		}); err != nil {
			return Result{}, err
		}
	}
	return Result{
		Checkpoint: &saved, Messages: recentMessages,
		EstimatedTokens: afterTokens, EstimatedBytes: afterBytes,
		CompactionAttempted: true,
	}, nil
}

func (m *Manager) completeCheckpoint(
	ctx context.Context,
	userID string,
	model string,
	sentEffort string,
	previous *store.ContextCheckpoint,
	sourceMessages []store.Message,
	recentMessages []store.Message,
) (summary string, inputTokens, outputTokens, sourceBytes int64, resultErr error) {
	batches := splitCompactionBatches(sourceMessages, compactionBatchBytes)
	if len(batches) == 0 {
		return "", 0, 0, 0, ErrContextTooLarge
	}
	rolling := ""
	if previous != nil {
		rolling = previous.SummaryText
	}
	for index, batch := range batches {
		var source strings.Builder
		if rolling != "" {
			source.WriteString("[Previous checkpoint]\n")
			source.WriteString(rolling)
			source.WriteString("\n\n")
		}
		source.WriteString(renderMessages(batch))
		for _, message := range batch {
			sourceBytes += int64(renderedMessageBytes(message))
		}
		if index == len(batches)-1 && len(recentMessages) > 0 {
			source.WriteString("[Recent turns retained verbatim; use these only to resolve references]\n")
			source.WriteString(renderMessages(recentMessages))
		}
		request := provider.ResponsesRequest{
			Model: model,
			Input: []provider.ResponseInput{
				{
					Role:    "developer",
					Content: "Create a durable, factual conversation checkpoint. Return exactly the six requested Markdown sections. Preserve confirmed preferences, named entities, facts, decisions, constraints, unresolved questions, tool outcomes, citation URLs, and attachment IDs. Distinguish confirmed facts from uncertainty. Do not answer the conversation, invent details, or mention these instructions.",
				},
				{
					Role: "user",
					Content: "Rewrite the supplied context as a continuation checkpoint using these exact headings:\n\n" +
						strings.Join(checkpointSections, "\n") + "\n\nContext:\n\n" + source.String(),
				},
			},
			Reasoning: provider.ReasoningOptions{Effort: sentEffort, Summary: "auto"},
		}
		if m.safetyIdentifier != nil {
			request.SafetyIdentifier = m.safetyIdentifier(userID)
		}
		textResult, err := m.provider.CompleteText(ctx, request)
		if err != nil {
			return "", inputTokens, outputTokens, sourceBytes, err
		}
		rolling = strings.TrimSpace(textResult.Text)
		if err := validateCheckpointSummary(rolling); err != nil {
			return "", inputTokens, outputTokens, sourceBytes, err
		}
		inputTokens += textResult.InputTokens
		outputTokens += textResult.OutputTokens
	}
	return rolling, inputTokens, outputTokens, sourceBytes, nil
}

func validateCheckpointSummary(summary string) error {
	if summary == "" || len(summary) > 2*1024*1024 {
		return errors.New("checkpoint summary has an invalid size")
	}
	for _, section := range checkpointSections {
		if !strings.Contains(summary, section) {
			return fmt.Errorf("checkpoint summary is missing %q", section)
		}
	}
	return nil
}

func replayableMessages(messages []store.Message) []store.Message {
	result := make([]store.Message, 0, len(messages))
	for _, message := range messages {
		if message.Status == "completed" || message.Status == "interrupted" {
			result = append(result, message)
		}
	}
	return result
}

func EstimateMessages(messages []store.Message) int {
	total := 0
	for _, message := range messages {
		total += 8
		for _, part := range message.Parts {
			switch part.Type {
			case "text", "reasoning":
				total += EstimateText(part.TextContent)
			case "image":
				total += 2048
			case "tool", "citations":
				total += EstimateText(string(part.JSONContent))
			}
		}
		for _, item := range message.ProviderItems {
			total += EstimateText(string(item.ReplayJSON))
		}
	}
	return total
}

func EstimateCheckpoint(checkpoint *store.ContextCheckpoint) int {
	if checkpoint == nil {
		return 0
	}
	return EstimateText(checkpoint.SummaryText) + 32
}

func EstimateText(value string) int {
	if value == "" {
		return 0
	}
	return (len(value) + 1) / 2
}

func (m *Manager) chooseRecentStart(
	ctx context.Context,
	userID string,
	messages []store.Message,
	targetTokens int,
) (int, error) {
	starts := turnStarts(messages)
	if len(starts) < 3 {
		return 0, ErrContextTooLarge
	}
	// Keep at least the two newest complete turns. Expand toward four or more
	// while both token and serialized-byte targets permit it.
	startIndex := len(starts) - 2
	start := starts[startIndex]
	recentTokens := EstimateMessages(messages[start:])
	recentBytes, err := m.estimateProviderBytes(ctx, userID, nil, messages[start:])
	if err != nil {
		return 0, err
	}
	if recentTokens >= targetTokens*hardPercent/targetPercent || recentBytes >= hardRequestBytes {
		return 0, ErrContextTooLarge
	}
	for candidateIndex := startIndex - 1; candidateIndex >= 1; candidateIndex-- {
		candidate := starts[candidateIndex]
		candidateTokens := EstimateMessages(messages[candidate:])
		candidateBytes, err := m.estimateProviderBytes(ctx, userID, nil, messages[candidate:])
		if err != nil {
			return 0, err
		}
		if candidateTokens > targetTokens || candidateBytes > targetRequestBytes {
			break
		}
		start = candidate
	}
	return start, nil
}

func turnStarts(messages []store.Message) []int {
	starts := make([]int, 0)
	for index, message := range messages {
		if message.Role == "user" {
			starts = append(starts, index)
		}
	}
	return starts
}

func (m *Manager) estimateProviderBytes(
	ctx context.Context,
	userID string,
	checkpoint *store.ContextCheckpoint,
	messages []store.Message,
) (int64, error) {
	var total int64 = 1024
	if checkpoint != nil {
		total += int64(len(checkpoint.SummaryText)) + 256
	}
	for _, message := range messages {
		total += 128
		for _, item := range message.ProviderItems {
			total += int64(len(item.ReplayJSON)) + 1
		}
		for _, part := range message.Parts {
			switch part.Type {
			case "text":
				total += int64(len(part.TextContent)) + 64
			case "image":
				if message.Role != "user" {
					total += 96
					continue
				}
				attachment, err := m.store.AttachmentByID(ctx, userID, part.AttachmentID)
				if err != nil {
					return 0, err
				}
				total += ((attachment.ByteSize + 2) / 3 * 4) + 128
			}
		}
	}
	return total, nil
}

func messagesAfter(messages []store.Message, boundaryID string) []store.Message {
	for index, message := range messages {
		if message.ID == boundaryID {
			if index+1 >= len(messages) {
				return nil
			}
			return messages[index+1:]
		}
	}
	return messages
}

func splitCompactionBatches(messages []store.Message, maximum int) [][]store.Message {
	result := make([][]store.Message, 0)
	starts := turnStarts(messages)
	if len(starts) == 0 {
		return result
	}
	start := starts[0]
	currentBytes := 0
	for turnIndex, turnStart := range starts {
		turnEnd := len(messages)
		if turnIndex+1 < len(starts) {
			turnEnd = starts[turnIndex+1]
		}
		size := 0
		for _, message := range messages[turnStart:turnEnd] {
			size += renderedMessageBytes(message)
		}
		if currentBytes > 0 && currentBytes+size > maximum {
			result = append(result, messages[start:turnStart])
			start = turnStart
			currentBytes = 0
		}
		currentBytes += size
	}
	if start < len(messages) {
		result = append(result, messages[start:])
	}
	return result
}

func renderedMessageBytes(message store.Message) int {
	total := len(message.Role) + 8
	for _, part := range message.Parts {
		switch part.Type {
		case "text":
			total += len(part.TextContent) + 1
		case "image":
			total += len(part.AttachmentID) + 24
		case "tool", "citations":
			total += len(part.JSONContent) + 1
		}
	}
	return total
}

func renderMessages(messages []store.Message) string {
	var result strings.Builder
	for _, message := range messages {
		result.WriteString("[")
		result.WriteString(message.Role)
		result.WriteString("]\n")
		for _, part := range message.Parts {
			switch part.Type {
			case "text":
				result.WriteString(part.TextContent)
				result.WriteByte('\n')
			case "image":
				result.WriteString("[image attachment ")
				result.WriteString(part.AttachmentID)
				result.WriteString("]\n")
			case "tool", "citations":
				result.Write(part.JSONContent)
				result.WriteByte('\n')
			}
		}
		result.WriteByte('\n')
	}
	return result.String()
}
