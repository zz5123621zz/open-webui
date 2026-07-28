export interface User {
  id: string;
  username: string;
  displayName: string;
  role: 'user' | 'admin';
  preferredModel?: string;
  createdAt: number;
  updatedAt: number;
}

export interface Model {
  id: string;
  name: string;
  description?: string;
  contextWindow: number;
  inputModalities?: string[];
  supportsWebSearch: boolean;
  imageGenerationMode?: 'responses_tool' | 'dedicated';
  reasoningEfforts?: string[];
  defaultReasoningEffort?: string;
  capabilitiesComplete: boolean;
  selectable: boolean;
}

export interface Conversation {
  id: string;
  ownerId?: string;
  ownerUsername?: string;
  ownerDisplayName?: string;
  title: string;
  model: string;
  reasoningEffort: string;
  createdAt: number;
  updatedAt: number;
  archivedAt?: number;
  pinnedAt?: number;
  retentionReason?: 'manual' | 'conversation_limit';
  archived?: boolean;
}

export interface StorageStatus {
  usedBytes: number;
  limitBytes: number;
  retainedBytes: number;
  activeConversations: number;
  maxActiveConversations: number;
  pinnedConversations: number;
  maxPinnedConversations: number;
  retentionDays: number;
}

export type ProgressiveSummaryMode = 'auto' | 'off';
export type ProgressiveSummaryState =
  | 'unknown'
  | 'probing'
  | 'active'
  | 'cooldown'
  | 'disabled'
  | 'mixed';

export interface ProgressiveSummaryModelStatus {
  model: string;
  state: Exclude<ProgressiveSummaryState, 'mixed' | 'disabled'>;
  lastCheckedAt?: number;
  cooldownUntil?: number;
}

export interface ProgressiveSummarySettings {
  mode: ProgressiveSummaryMode;
  hardDisabled: boolean;
  effectiveState: ProgressiveSummaryState;
  models: ProgressiveSummaryModelStatus[];
  updatedAt: number;
}

export type SpeechMode = 'manual' | 'auto';

export interface SpeechVoice {
  id: string;
  label: string;
}

export interface SpeechPreference {
  mode: SpeechMode;
  autoRead: boolean;
  speed: number;
  voice: string;
  effectiveVoice: string;
  updatedAt: number;
  serviceEnabled: boolean;
  provider: string;
  providerConfigured: boolean;
  voices: SpeechVoice[];
  audioAuthorization: 'required_on_each_device';
}

export interface SpeechProviderDescriptor {
  id: string;
  configured: boolean;
  voices: SpeechVoice[];
}

export interface SpeechServiceSettings {
  enabled: boolean;
  provider: string;
  defaultVoice: string;
  updatedAt: number;
  providers: SpeechProviderDescriptor[];
  concurrency: {
    perUser: number;
    global: number;
  };
}

export type Workbench = 'general' | 'restaurant';

export interface WorkbenchSetting {
  effective: Workbench;
  initial: Workbench;
  preference?: Workbench;
}

export interface WorkbenchResponse {
  workbench: WorkbenchSetting;
  guidanceEnabled: boolean;
}

export interface RestaurantProfileFact {
  field: string;
  value: string;
  updatedAt: number;
}

export interface ClarificationOption {
  key: string;
  label: string;
  description?: string | null;
}

export interface ClarificationQuestion {
  key: string;
  prompt: string;
  selection: 'single_select' | 'multi_select';
  options: ClarificationOption[];
  allowOther: boolean;
  allowDelegatedDefault: boolean;
  minimumSelections?: number | null;
  maximumSelections?: number | null;
}

export interface ClarificationCardsData {
  schemaVersion: 1;
  instanceId: string;
  intro?: string | null;
  currentUnderstanding: string[];
  questions: ClarificationQuestion[];
}

export interface ProfileUpdateProposal {
  field: string;
  operation: 'set' | 'replace' | 'delete';
  proposedValue?: string | null;
  reason: string;
}

export interface TaskBriefData {
  schemaVersion: 1;
  instanceId: string;
  goal: string;
  context: string[];
  constraints: string[];
  desiredOutput: string[];
  delegatedAssumptions: string[];
  unresolved: string[];
  profileUpdateProposal?: ProfileUpdateProposal | null;
}

export interface ClarificationAnswer {
  questionKey: string;
  selectedOptionKeys: string[];
  otherText?: string;
  delegatedDefault?: boolean;
}

export type GuidanceIntent =
  | 'continue_refining'
  | 'generate_from_current'
  | 'confirm_brief'
  | 'add_context';

export interface GuidanceSubmission {
  sourceAssistantMessageId: string;
  sourcePartId: string;
  intent: GuidanceIntent;
  answers: ClarificationAnswer[];
  profileDecision?: 'save' | 'current_task_only' | 'ignore';
  additionalText?: string;
}

export interface MessagePart {
  id?: string;
  sequence?: number;
  type:
    | 'text'
    | 'reasoning'
    | 'tool'
    | 'citations'
    | 'image'
    | 'clarification'
    | 'clarification_submission'
    | 'task_brief'
    | 'guidance_error';
  text?: string;
  data?: Record<string, unknown>;
  attachmentId?: string;
  createdAt?: number;
}

export interface Message {
  id: string;
  conversationId: string;
  role: 'user' | 'assistant';
  model?: string;
  reasoningEffortRequested?: string;
  reasoningEffortSent?: string;
  status: 'pending' | 'streaming' | 'completed' | 'interrupted' | 'error';
  parentMessageId?: string;
  providerResponseId?: string;
  inputTokens?: number;
  outputTokens?: number;
  reasoningTokens?: number;
  errorCode?: string;
  createdAt: number;
  completedAt?: number;
  parts: MessagePart[];
}

export interface Attachment {
  id: string;
  kind: 'upload' | 'generated';
  originalName?: string;
  mediaType: string;
  byteSize: number;
  sha256: string;
  createdAt: number;
  url: string;
}

export interface ContextCheckpoint {
  id: string;
  conversationId: string;
  boundaryMessageId: string;
  previousCheckpointId?: string;
  model: string;
  summaryText: string;
  sourceFirstMessageId: string;
  sourceLastMessageId: string;
  estimatedTokensBefore: number;
  estimatedTokensAfter: number;
  sourceBytes: number;
  inputTokens?: number;
  outputTokens?: number;
  status: string;
  createdAt: number;
}

export interface ConversationSearchResult {
  conversation: Conversation;
  snippet?: string;
  matchedIn: 'title' | 'message';
}

export interface UsageRow {
  month: string;
  model: string;
  ownerId?: string;
  ownerUsername?: string;
  ownerDisplayName?: string;
  responses: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
}

export interface StreamEvent {
  event: string;
  data: Record<string, any>;
}

export interface APIErrorBody {
  error?: {
    code?: string;
    message?: string;
  };
}
