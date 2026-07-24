export interface User {
  id: string;
  username: string;
  displayName: string;
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
  reasoningEfforts?: string[];
  defaultReasoningEffort?: string;
  capabilitiesComplete: boolean;
  selectable: boolean;
}

export interface Conversation {
  id: string;
  title: string;
  model: string;
  reasoningEffort: string;
  createdAt: number;
  updatedAt: number;
  archivedAt?: number;
  archived?: boolean;
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
