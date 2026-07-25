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

export interface MessagePart {
  id?: string;
  sequence?: number;
  type: 'text' | 'reasoning' | 'tool' | 'citations' | 'image';
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
