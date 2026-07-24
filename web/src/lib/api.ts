import type {
  APIErrorBody,
  Attachment,
  Conversation,
  ContextCheckpoint,
  Message,
  Model,
  StreamEvent,
  User
} from './types';

let csrfToken = '';

export class APIError extends Error {
  code: string;
  status: number;

  constructor(message: string, code = 'request_failed', status = 0) {
    super(message);
    this.name = 'APIError';
    this.code = code;
    this.status = status;
  }
}

async function parseError(response: Response): Promise<APIError> {
  let body: APIErrorBody = {};
  try {
    body = (await response.json()) as APIErrorBody;
  } catch {
    // The server intentionally does not expose an upstream error body.
  }
  return new APIError(
    body.error?.message || `请求失败（HTTP ${response.status}）`,
    body.error?.code || 'request_failed',
    response.status
  );
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method || 'GET').toUpperCase();
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  if (init.body && !(init.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json');
  }
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrfToken) {
    headers.set('X-CSRF-Token', csrfToken);
  }
  const response = await fetch(path, {
    ...init,
    headers,
    credentials: 'same-origin'
  });
  if (!response.ok) {
    throw await parseError(response);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

export async function getSession(): Promise<User> {
  const body = await request<{ user: User; csrfToken: string }>('/api/v1/me');
  csrfToken = body.csrfToken;
  return body.user;
}

export async function login(username: string, password: string): Promise<User> {
  const body = await request<{ user: User; csrfToken: string }>('/api/v1/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password })
  });
  csrfToken = body.csrfToken;
  return body.user;
}

export async function logout(): Promise<void> {
  await request<void>('/api/v1/auth/logout', { method: 'POST' });
  csrfToken = '';
}

export async function logoutAll(): Promise<void> {
  await request<void>('/api/v1/auth/logout-all', { method: 'POST' });
  csrfToken = '';
}

export async function changePassword(
  currentPassword: string,
  newPassword: string
): Promise<void> {
  await request<void>('/api/v1/me/password', {
    method: 'PUT',
    body: JSON.stringify({ currentPassword, newPassword })
  });
  csrfToken = '';
}

export async function getModels(): Promise<Model[]> {
  const body = await request<{ models: Model[] }>('/api/v1/models');
  return body.models;
}

export async function getConversations(archived = false): Promise<Conversation[]> {
  const body = await request<{ conversations: Conversation[] }>(
    `/api/v1/conversations${archived ? '?archived=true' : ''}`
  );
  return body.conversations;
}

export async function createConversation(
  model: string,
  reasoningEffort = 'auto',
  title = 'New chat'
): Promise<Conversation> {
  const body = await request<{ conversation: Conversation }>('/api/v1/conversations', {
    method: 'POST',
    body: JSON.stringify({ model, reasoningEffort, title })
  });
  return body.conversation;
}

export async function updateConversation(
  id: string,
  patch: Partial<Pick<Conversation, 'title' | 'model' | 'reasoningEffort' | 'archived'>>
): Promise<Conversation> {
  return (await updateConversationWithMeta(id, patch)).conversation;
}

export async function updateConversationWithMeta(
  id: string,
  patch: Partial<Pick<Conversation, 'title' | 'model' | 'reasoningEffort' | 'archived'>>
): Promise<{ conversation: Conversation; reasoningEffortReset?: boolean }> {
  return request<{ conversation: Conversation; reasoningEffortReset?: boolean }>(
    `/api/v1/conversations/${id}`,
    {
    method: 'PATCH',
    body: JSON.stringify(patch)
    }
  );
}

export async function deleteConversation(id: string): Promise<void> {
  await request<void>(`/api/v1/conversations/${id}`, { method: 'DELETE' });
}

export async function getMessages(conversationId: string): Promise<Message[]> {
  const body = await request<{ messages: Message[] }>(
    `/api/v1/conversations/${conversationId}/messages`
  );
  return body.messages;
}

export async function getContextCheckpoints(conversationId: string): Promise<ContextCheckpoint[]> {
  const body = await request<{ checkpoints: ContextCheckpoint[] }>(
    `/api/v1/conversations/${encodeURIComponent(conversationId)}/context-checkpoints`
  );
  return body.checkpoints;
}

export async function uploadAttachment(file: File): Promise<Attachment> {
  const form = new FormData();
  form.set('file', file, file.name);
  const body = await request<{ attachment: Omit<Attachment, 'url'>; url: string }>(
    '/api/v1/attachments',
    { method: 'POST', body: form }
  );
  return { ...body.attachment, url: body.url };
}

export async function deleteAttachment(id: string): Promise<void> {
  await request<void>(`/api/v1/attachments/${id}`, { method: 'DELETE' });
}

export async function streamResponse(
  conversationId: string,
  text: string,
  attachmentIds: string[],
  requestId: string,
  onEvent: (item: StreamEvent) => void,
  signal?: AbortSignal
): Promise<void> {
  const response = await fetch(`/api/v1/conversations/${conversationId}/responses`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'text/event-stream',
      'X-CSRF-Token': csrfToken
    },
    body: JSON.stringify({ text, attachmentIds, requestId }),
    signal
  });
  await consumeEventStream(response, onEvent);
}

export async function regenerateResponse(
  messageId: string,
  requestId: string,
  onEvent: (item: StreamEvent) => void,
  signal?: AbortSignal
): Promise<void> {
  const response = await fetch(`/api/v1/messages/${messageId}/regenerate`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      Accept: 'text/event-stream',
      'X-CSRF-Token': csrfToken
    },
    body: JSON.stringify({ requestId }),
    signal
  });
  await consumeEventStream(response, onEvent);
}

export async function cancelResponse(messageId: string): Promise<void> {
  await request<void>(`/api/v1/responses/${encodeURIComponent(messageId)}/cancel`, {
    method: 'POST'
  });
}

async function consumeEventStream(
  response: Response,
  onEvent: (item: StreamEvent) => void
): Promise<void> {
  if (!response.ok) {
    throw await parseError(response);
  }
  if (!response.body) {
    throw new APIError('浏览器未收到响应流。', 'stream_unavailable', response.status);
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';

  const dispatch = (block: string) => {
    let event = 'message';
    const dataLines: string[] = [];
    for (const line of block.split('\n')) {
      if (line.startsWith('event:')) event = line.slice(6).trim();
      if (line.startsWith('data:')) dataLines.push(line.slice(5).replace(/^\s/, ''));
    }
    if (!dataLines.length) return;
    try {
      onEvent({ event, data: JSON.parse(dataLines.join('\n')) });
    } catch {
      throw new APIError('服务端发送了无法解析的流事件。', 'invalid_stream_event');
    }
  };

  for (;;) {
    const { value, done } = await reader.read();
    buffer += decoder.decode(value, { stream: !done }).replace(/\r\n/g, '\n');
    let separator = buffer.indexOf('\n\n');
    while (separator >= 0) {
      const block = buffer.slice(0, separator);
      buffer = buffer.slice(separator + 2);
      if (block.trim()) dispatch(block);
      separator = buffer.indexOf('\n\n');
    }
    if (done) break;
  }
  if (buffer.trim()) dispatch(buffer);
}

export function attachmentURL(id: string): string {
  return `/api/v1/attachments/${encodeURIComponent(id)}/content`;
}
