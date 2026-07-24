<script lang="ts">
  import { onMount, tick } from 'svelte';
  import {
    APIError,
    cancelResponse,
    changePassword,
    createConversation,
    deleteAttachment,
    deleteConversation,
    getConversations,
    getContextCheckpoints,
    getMessages,
    getModels,
    getSession,
    login,
    logout,
    logoutAll,
    regenerateResponse,
    streamResponse,
    updateConversation,
    updateConversationWithMeta,
    uploadAttachment
  } from './lib/api';
  import { locale, setLocale, translate } from './lib/i18n';
  import Icon from './lib/Icon.svelte';
  import MessageView from './lib/MessageView.svelte';
  import type {
    Attachment,
    Conversation,
    ContextCheckpoint,
    Message,
    MessagePart,
    Model,
    StreamEvent,
    User
  } from './lib/types';

  type Phase = 'boot' | 'login' | 'ready';

  let phase: Phase = 'boot';
  let user: User | null = null;
  let models: Model[] = [];
  let conversations: Conversation[] = [];
  let activeConversationId = '';
  let messages: Message[] = [];
  let checkpoints: ContextCheckpoint[] = [];
  let draftModel = '';
  let draftReasoningEffort = 'high';
  let text = '';
  let uploads: Attachment[] = [];
  let generateImage = false;
  let uploading = false;
  let generating = false;
  let loadingMessages = false;
  let workspaceError = '';
  let loginError = '';
  let contextStatus = '';
  let sidebarOpen = false;
  let profileOpen = false;
  let dialog: '' | 'security' | 'about' = '';
  let accountError = '';
  let accountPending = false;
  let showArchived = false;
  let abortController: AbortController | null = null;
  let activeAssistantId = '';
  let scrollElement: HTMLDivElement;
  let textareaElement: HTMLTextAreaElement;
  let fileElement: HTMLInputElement;
  let dialogElement: HTMLDivElement;
  let scrollQueued = false;
  let editingTitleId = '';
  let editingTitle = '';
  let modelPickerOpen = false;
  let modelSearch = '';
  type Theme = 'light' | 'dark' | 'system';
  const savedTheme = localStorage.getItem('personal-chat-theme');
  let theme: Theme =
    savedTheme === 'light' || savedTheme === 'dark' || savedTheme === 'system'
      ? savedTheme
      : 'light';
  let resolvedTheme: 'light' | 'dark' = 'light';

  $: activeConversation =
    conversations.find((conversation) => conversation.id === activeConversationId) || null;
  $: activeModel =
    models.find((model) => model.id === (activeConversation?.model || draftModel)) || null;
  $: effortOptions = Array.from(
    new Set(['auto', ...(activeModel?.reasoningEfforts || [])])
  );
  $: selectedReasoningEffort =
    activeConversation?.reasoningEffort || draftReasoningEffort;
  $: selectableModels = models.filter((model) => model.selectable);
  $: filteredModels = selectableModels.filter((model) => {
    const query = modelSearch.trim().toLocaleLowerCase();
    return !query ||
      model.name.toLocaleLowerCase().includes(query) ||
      model.id.toLocaleLowerCase().includes(query);
  });
  $: t = (chinese: string, english: string) => translate($locale, chinese, english);

  onMount(() => {
    setLocale($locale);
    const systemTheme = matchMedia('(prefers-color-scheme: dark)');
    const updateSystemTheme = () => applyTheme();
    systemTheme.addEventListener('change', updateSystemTheme);
    applyTheme();
    void initialize();
    return () => systemTheme.removeEventListener('change', updateSystemTheme);
  });

  async function initialize() {
    try {
      user = await getSession();
      await loadWorkspace();
      phase = 'ready';
    } catch (error) {
      if (error instanceof APIError && error.status === 401) {
        phase = 'login';
      } else {
        workspaceError = errorMessage(error);
        phase = 'login';
      }
    }
  }

  function errorMessage(error: unknown): string {
    if (error instanceof APIError) return localizedAPIError(error.code, error.message);
    if (error instanceof Error && error.name === 'AbortError') return '';
    if (error instanceof TypeError) {
      return t(
        '网络连接中断，已尝试从服务器恢复最新状态。',
        'The network connection was interrupted. The latest server state was restored when possible.'
      );
    }
    return error instanceof Error ? error.message : t('发生了未知错误。', 'An unknown error occurred.');
  }

  function localizedAPIError(code: string, fallback: string): string {
    const messages: Record<string, [string, string]> = {
      invalid_credentials: ['用户名或密码错误。', 'Invalid username or password.'],
      invalid_current_password: ['当前密码不正确。', 'The current password is incorrect.'],
      invalid_password: ['密码不符合安全要求。', 'The password does not meet the security requirements.'],
      login_rate_limited: ['登录尝试过多，请稍后再试。', 'Too many login attempts. Try again later.'],
      authentication_required: ['请重新登录。', 'Please sign in again.'],
      rate_limited: ['请求过于频繁，请稍后再试。', 'Too many requests. Try again shortly.'],
      provider_unavailable: ['模型目录暂时不可用。', 'The model catalog is temporarily unavailable.'],
      provider_invalid_response: ['模型目录返回了无效响应。', 'The model catalog returned an invalid response.'],
      no_model_available: ['当前没有可用的聊天模型。', 'No chat model is currently available.'],
      provider_model_unavailable: ['所选模型当前不可用。', 'The selected model is currently unavailable.'],
      reasoning_effort_unsupported: [
        '当前模型不支持所选推理强度，已保留原设置。',
        'The current model does not support the selected reasoning effort.'
      ],
      conversation_busy: ['此对话正在生成回答。', 'This chat is currently generating a response.'],
      upload_in_progress: ['同一时间只能上传一张图片。', 'Only one upload can run at a time.'],
      upload_too_large: ['每张图片不能超过 12 MiB。', 'Each image must be 12 MiB or smaller.'],
      unsupported_image: ['仅支持 PNG、JPEG 和 WebP 图片。', 'Only PNG, JPEG, and WebP images are supported.'],
      invalid_image: ['上传的文件不是有效图片。', 'The uploaded file is not a valid image.'],
      attachment_in_use: ['这张图片已用于消息，无法删除。', 'This image is already used by a message.'],
      message_required: ['请输入文字或上传图片。', 'Enter a message or attach an image.'],
      message_too_large: ['消息文字过长。', 'The message text is too large.'],
      too_many_images: ['每条消息最多包含四张图片。', 'A message can contain at most four images.'],
      model_image_input_unsupported: [
        '当前模型不支持图片输入。',
        'The current model does not support image input.'
      ],
      model_image_generation_unsupported: [
        '当前模型不支持图片生成，请更换模型。',
        'The current model does not support image generation. Choose another model.'
      ],
      image_prompt_required: ['请输入图片描述。', 'Enter a prompt for the image.'],
      image_prompt_too_long: [
        '图片描述超过 CPA 的 8000 字节限制，请精简后重试。',
        'The image prompt exceeds CPA’s 8000-byte limit. Shorten it and try again.'
      ],
      image_generation_attachments_unsupported: [
        '生成图片模式暂不支持同时上传参考图。',
        'Image generation mode does not support uploaded reference images yet.'
      ],
      generated_image_invalid: [
        '图片服务返回了无法安全保存的图片。',
        'The image service returned an image that could not be saved safely.'
      ],
      stream_incomplete: [
        '连接在回答完成前中断，已尝试恢复最新状态。',
        'The connection ended before completion. The latest state was restored when possible.'
      ],
      too_many_requests: ['当前请求较多，请稍后再试。', 'The request queue is full. Try again shortly.'],
      provider_queue_timeout: ['排队等待超时，请重试。', 'The request timed out while queued. Try again.'],
      provider_request_too_large: [
        '当前对话编译后超过 50 MiB，请减少本轮图片或开启新对话。',
        'The compiled conversation exceeds 50 MiB. Remove images from this turn or start a new chat.'
      ],
      internal_error: ['服务器发生内部错误。', 'The server encountered an internal error.']
    };
    const message = messages[code];
    return message ? t(message[0], message[1]) : fallback;
  }

  function effortForModel(model: Model | undefined, requested: string): string {
    if (!model) return 'auto';
    const supported = new Set(['auto', ...(model.reasoningEfforts || [])]);
    if (supported.has(requested)) return requested;
    if (model.defaultReasoningEffort && supported.has(model.defaultReasoningEffort)) {
      return model.defaultReasoningEffort;
    }
    if (supported.has('high')) return 'high';
    return 'auto';
  }

  function setDraftModel(modelId: string) {
    draftModel = modelId;
    const model = models.find((item) => item.id === modelId);
    draftReasoningEffort = effortForModel(model, draftReasoningEffort);
    if (!model?.imageGenerationMode) generateImage = false;
  }

  async function loadWorkspace() {
    const [loadedModels, loadedConversations] = await Promise.all([
      getModels(),
      getConversations(false)
    ]);
    models = loadedModels;
    conversations = loadedConversations;
    const initialDraftModel =
      (user?.preferredModel &&
        loadedModels.find(
          (model) => model.selectable && model.id === user?.preferredModel
        )?.id) ||
      loadedModels.find((model) => model.selectable)?.id ||
      '';
    draftModel = initialDraftModel;
    draftReasoningEffort = effortForModel(
      loadedModels.find((model) => model.id === initialDraftModel),
      draftReasoningEffort
    );

    const remembered = localStorage.getItem('personal-chat-conversation');
    const initial =
      loadedConversations.find((conversation) => conversation.id === remembered) ||
      loadedConversations[0];
    if (initial) await openConversation(initial.id);
  }

  async function refreshModels() {
    profileOpen = false;
    workspaceError = '';
    try {
      const loaded = await getModels();
      models = loaded;
      if (!activeConversation) {
        const nextDraftModel = loaded.some(
          (model) => model.selectable && model.id === draftModel
        )
          ? draftModel
          : loaded.find((model) => model.selectable)?.id || '';
        setDraftModel(nextDraftModel);
      }
    } catch (error) {
      workspaceError = errorMessage(error);
    }
  }

  async function submitLogin(event: SubmitEvent) {
    const form = event.currentTarget as HTMLFormElement;
    const data = new FormData(form);
    loginError = '';
    form.classList.add('pending');
    try {
      user = await login(String(data.get('username') || ''), String(data.get('password') || ''));
      await loadWorkspace();
      phase = 'ready';
    } catch (error) {
      loginError = errorMessage(error);
    } finally {
      form.classList.remove('pending');
    }
  }

  async function doLogout() {
    try {
      await logout();
    } finally {
      abortController?.abort();
      user = null;
      messages = [];
      checkpoints = [];
      conversations = [];
      profileOpen = false;
      phase = 'login';
    }
  }

  function clearWorkspaceAndShowLogin() {
    abortController?.abort();
    user = null;
    messages = [];
    checkpoints = [];
    conversations = [];
    profileOpen = false;
    dialog = '';
    phase = 'login';
  }

  async function submitPasswordChange(event: SubmitEvent) {
    const form = event.currentTarget as HTMLFormElement;
    const data = new FormData(form);
    const currentPassword = String(data.get('currentPassword') || '');
    const newPassword = String(data.get('newPassword') || '');
    const confirmation = String(data.get('confirmation') || '');
    accountError = '';
    if (newPassword !== confirmation) {
      accountError = t('两次输入的新密码不一致。', 'The new passwords do not match.');
      return;
    }
    accountPending = true;
    try {
      await changePassword(currentPassword, newPassword);
      clearWorkspaceAndShowLogin();
    } catch (error) {
      accountError = errorMessage(error);
    } finally {
      accountPending = false;
    }
  }

  async function doLogoutAll() {
    accountError = '';
    accountPending = true;
    try {
      await logoutAll();
      clearWorkspaceAndShowLogin();
    } catch (error) {
      accountError = errorMessage(error);
    } finally {
      accountPending = false;
    }
  }

  async function openDialog(value: 'security' | 'about') {
    profileOpen = false;
    accountError = '';
    dialog = value;
    await tick();
    dialogElement?.focus();
  }

  async function openConversation(id: string) {
    if (generating || id === activeConversationId && messages.length) {
      sidebarOpen = false;
      return;
    }
    activeConversationId = id;
    const conversation = conversations.find((item) => item.id === id);
    const conversationModel = models.find((item) => item.id === conversation?.model);
    if (!conversationModel?.imageGenerationMode) generateImage = false;
    localStorage.setItem('personal-chat-conversation', id);
    loadingMessages = true;
    workspaceError = '';
    sidebarOpen = false;
    try {
      [messages, checkpoints] = await Promise.all([getMessages(id), getContextCheckpoints(id)]);
      await scrollToBottom(false);
    } catch (error) {
      workspaceError = errorMessage(error);
    } finally {
      loadingMessages = false;
    }
  }

  async function newConversation(): Promise<Conversation | null> {
    if (generating) return null;
    workspaceError = '';
    try {
      const wasShowingArchived = showArchived;
      const conversation = await createConversation(
        activeConversation?.model || draftModel,
        activeConversation?.reasoningEffort || draftReasoningEffort
      );
      showArchived = false;
      conversations = wasShowingArchived ? [conversation] : [conversation, ...conversations];
      activeConversationId = conversation.id;
      messages = [];
      checkpoints = [];
      contextStatus = '';
      localStorage.setItem('personal-chat-conversation', conversation.id);
      sidebarOpen = false;
      await tick();
      textareaElement?.focus();
      return conversation;
    } catch (error) {
      workspaceError = errorMessage(error);
      return null;
    }
  }

  async function toggleArchiveView() {
    if (generating) return;
    showArchived = !showArchived;
    profileOpen = false;
    workspaceError = '';
    activeConversationId = '';
    messages = [];
    checkpoints = [];
    try {
      conversations = await getConversations(showArchived);
    } catch (error) {
      workspaceError = errorMessage(error);
    }
  }

  async function setArchived(event: Event, conversation: Conversation, archived: boolean) {
    event.stopPropagation();
    if (generating && conversation.id === activeConversationId) return;
    try {
      await updateConversation(conversation.id, { archived });
      conversations = conversations.filter((item) => item.id !== conversation.id);
      if (conversation.id === activeConversationId) {
        activeConversationId = '';
        messages = [];
        checkpoints = [];
      }
    } catch (error) {
      workspaceError = errorMessage(error);
    }
  }

  async function removeConversation(event: Event, conversation: Conversation) {
    event.stopPropagation();
    if (generating && conversation.id === activeConversationId) return;
    if (!confirm(t(
      `删除“${conversation.title}”及其全部消息？`,
      `Delete “${conversation.title}” and all of its messages?`
    ))) return;
    try {
      await deleteConversation(conversation.id);
      conversations = conversations.filter((item) => item.id !== conversation.id);
      if (conversation.id === activeConversationId) {
        const next = conversations[0];
        activeConversationId = '';
        messages = [];
        checkpoints = [];
        if (next) await openConversation(next.id);
      }
    } catch (error) {
      workspaceError = errorMessage(error);
    }
  }

  function beginRename(event: Event, conversation: Conversation) {
    event.stopPropagation();
    editingTitleId = conversation.id;
    editingTitle = conversation.title;
  }

  async function finishRename(conversation: Conversation) {
    const title = editingTitle.trim();
    editingTitleId = '';
    if (!title || title === conversation.title) return;
    try {
      const updated = await updateConversation(conversation.id, { title });
      replaceConversation(updated);
    } catch (error) {
      workspaceError = errorMessage(error);
    }
  }

  async function selectModel(model: string) {
    modelPickerOpen = false;
    modelSearch = '';
    if (!activeConversation) {
      setDraftModel(model);
      return;
    }
    if (generating) return;
    try {
      const result = await updateConversationWithMeta(activeConversation.id, { model });
      replaceConversation(result.conversation);
      if (!models.find((item) => item.id === model)?.imageGenerationMode) {
        generateImage = false;
      }
      if (result.reasoningEffortReset) {
        contextStatus = t(
          '新模型不支持原推理强度，已重置为自动。',
          'The new model does not support the previous reasoning effort, so it was reset to Auto.'
        );
      }
    } catch (error) {
      workspaceError = errorMessage(error);
    }
  }

  function toggleModelPicker() {
    if (generating || showArchived || !selectableModels.length) return;
    modelPickerOpen = !modelPickerOpen;
    modelSearch = '';
  }

  async function changeEffort(event: Event) {
    if (generating) return;
    const reasoningEffort = (event.currentTarget as HTMLSelectElement).value;
    if (!activeConversation) {
      draftReasoningEffort = reasoningEffort;
      return;
    }
    try {
      const updated = await updateConversation(activeConversation.id, { reasoningEffort });
      replaceConversation(updated);
    } catch (error) {
      workspaceError = errorMessage(error);
    }
  }

  function toggleImageGeneration() {
    if (generating || showArchived) return;
    if (!activeModel?.imageGenerationMode) {
      workspaceError = localizedAPIError(
        'model_image_generation_unsupported',
        t('当前模型不支持图片生成。', 'The current model does not support image generation.')
      );
      return;
    }
    if (uploads.length > 0) {
      workspaceError = localizedAPIError(
        'image_generation_attachments_unsupported',
        t(
          '生成图片模式暂不支持同时上传参考图。',
          'Image generation mode does not support uploaded reference images yet.'
        )
      );
      return;
    }
    workspaceError = '';
    generateImage = !generateImage;
    textareaElement?.focus();
  }

  function prepareImagePrompt() {
    if (!activeModel?.imageGenerationMode) {
      workspaceError = localizedAPIError(
        'model_image_generation_unsupported',
        t('当前模型不支持图片生成。', 'The current model does not support image generation.')
      );
      return;
    }
    if (uploads.length > 0) {
      workspaceError = localizedAPIError(
        'image_generation_attachments_unsupported',
        t(
          '生成图片模式暂不支持同时上传参考图。',
          'Image generation mode does not support uploaded reference images yet.'
        )
      );
      return;
    }
    workspaceError = '';
    generateImage = true;
    text = t(
      '请生成一张温暖、梦幻风格的插画',
      'Create a warm, dreamlike illustration'
    );
    resizeComposer();
    textareaElement?.focus();
  }

  function replaceConversation(updated: Conversation) {
    conversations = conversations
      .map((conversation) => (conversation.id === updated.id ? updated : conversation))
      .sort((left, right) => right.updatedAt - left.updatedAt);
  }

  async function chooseFiles(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const files = Array.from(input.files || []);
    input.value = '';
    if (!files.length) return;
    if (generateImage) {
      workspaceError = localizedAPIError(
        'image_generation_attachments_unsupported',
        t(
          '生成图片模式暂不支持同时上传参考图。',
          'Image generation mode does not support uploaded reference images yet.'
        )
      );
      return;
    }
    if (uploads.length + files.length > 4) {
      workspaceError = t('每条消息最多上传 4 张图片。', 'You can attach up to 4 images per message.');
      return;
    }
    uploading = true;
    workspaceError = '';
    try {
      for (const file of files) {
        if (!['image/png', 'image/jpeg', 'image/webp'].includes(file.type)) {
          throw new APIError(
            t(`不支持 ${file.name} 的图片格式。`, `${file.name} uses an unsupported image format.`),
            'unsupported_image'
          );
        }
        if (file.size > 12 * 1024 * 1024) {
          throw new APIError(
            t(`${file.name} 超过 12 MiB。`, `${file.name} exceeds 12 MiB.`),
            'upload_too_large'
          );
        }
        uploads = [...uploads, await uploadAttachment(file)];
      }
    } catch (error) {
      workspaceError = errorMessage(error);
    } finally {
      uploading = false;
    }
  }

  async function removeUpload(attachment: Attachment) {
    uploads = uploads.filter((item) => item.id !== attachment.id);
    try {
      await deleteAttachment(attachment.id);
    } catch {
      // It may already be bound if the response started; the preview can still disappear.
    }
  }

  function resizeComposer() {
    if (!textareaElement) return;
    textareaElement.style.height = '0px';
    textareaElement.style.height = `${Math.min(textareaElement.scrollHeight, 190)}px`;
  }

  function composerKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
      event.preventDefault();
      void send();
    }
  }

  async function reconcileStreamFailure(
    conversationId: string,
    assistantId: string
  ): Promise<Message | undefined> {
    for (const delay of [0, 250, 750, 1500, 2500]) {
      if (delay > 0) await new Promise((resolve) => setTimeout(resolve, delay));
      try {
        const latest = await getMessages(conversationId);
        messages = latest;
        const restored = latest.find((message) => message.id === assistantId);
        if (restored && restored.status !== 'streaming' && restored.status !== 'pending') {
          return restored;
        }
      } catch {
        return undefined;
      }
    }
    return undefined;
  }

  async function send() {
    const outgoingText = text.trim();
    const outgoingUploads = [...uploads];
    const outgoingGenerateImage = generateImage;
    if (generating || uploading || (!outgoingText && outgoingUploads.length === 0)) return;

    let conversation = activeConversation;
    if (!conversation) conversation = await newConversation();
    if (!conversation) return;

    generating = true;
    workspaceError = '';
    contextStatus = '';
    text = '';
    uploads = [];
    generateImage = false;
    await tick();
    resizeComposer();
    abortController = new AbortController();

    let assistantId = '';
    try {
      await streamResponse(
        conversation.id,
        outgoingText,
        outgoingUploads.map((attachment) => attachment.id),
        crypto.randomUUID(),
        outgoingGenerateImage,
        (item) => {
          if (item.event === 'response.queued') {
            contextStatus = t(
              `请求排队中（第 ${Number(item.data.position || 1)} 位）`,
              `Request queued (position ${Number(item.data.position || 1)})`
            );
          } else if (item.event === 'response.started') {
            contextStatus = '';
            const userMessage = item.data.userMessage as Message | undefined;
            const assistantMessage = item.data.assistantMessage as Message;
            assistantId = assistantMessage.id;
            activeAssistantId = assistantMessage.id;
            messages = userMessage
              ? [...messages, userMessage, assistantMessage]
              : [...messages, assistantMessage];
            if (conversation?.title === 'New chat' && outgoingText) {
              const title = titleFrom(outgoingText);
              void updateConversation(conversation.id, { title }).then(replaceConversation);
            }
          } else if (item.event === 'response.reasoning.delta') {
            appendTextPart(assistantId, 'reasoning', String(item.data.delta || ''));
          } else if (item.event === 'response.text.delta') {
            appendTextPart(assistantId, 'text', String(item.data.delta || ''));
          } else if (item.event === 'response.tool') {
            updateToolPart(assistantId, item.data);
          } else if (item.event === 'response.image' && item.data.attachmentId) {
            appendImagePart(assistantId, String(item.data.attachmentId));
          } else if (item.event === 'response.context') {
            contextStatus = contextLabel(String(item.data.status || ''));
            if (item.data.status === 'completed') void refreshCheckpoints(conversation.id);
          } else if (item.event === 'response.completed') {
            replaceMessage(item.data.message as Message);
            contextStatus = '';
          } else if (item.event === 'response.error') {
            if (item.data.messageRecord) replaceMessage(item.data.messageRecord as Message);
            workspaceError = localizedAPIError(
              String(item.data.code || ''),
              String(item.data.message || t('回答生成失败。', 'Response generation failed.'))
            );
          }
          queueScroll();
        },
        abortController.signal
      );
    } catch (error) {
      const message = errorMessage(error);
      if (assistantId) {
        const restored = await reconcileStreamFailure(conversation.id, assistantId);
        if (restored?.status === 'completed') {
          workspaceError = '';
        } else {
          if (message) workspaceError = message;
          if (!restored) {
            messages = messages.map((item) =>
              item.id === assistantId ? { ...item, status: 'interrupted' } : item
            );
          }
        }
      } else {
        if (message) workspaceError = message;
        text = outgoingText;
        uploads = outgoingUploads;
        generateImage = outgoingGenerateImage;
      }
    } finally {
      generating = false;
      abortController = null;
      activeAssistantId = '';
      contextStatus = '';
      await refreshCheckpoints(conversation.id);
      try {
        const latest = await getConversations(showArchived);
        conversations = latest;
      } catch {
        // The current conversation remains usable if refreshing the sidebar fails.
      }
      queueScroll();
    }
  }

  async function regenerate(message: Message) {
    if (generating || !activeConversation) return;
    generating = true;
    workspaceError = '';
    contextStatus = '';
    abortController = new AbortController();
    let assistantId = '';
    try {
      await regenerateResponse(
        message.id,
        crypto.randomUUID(),
        (item) => {
          if (item.event === 'response.queued') {
            contextStatus = t(
              `请求排队中（第 ${Number(item.data.position || 1)} 位）`,
              `Request queued (position ${Number(item.data.position || 1)})`
            );
          } else if (item.event === 'response.started') {
            contextStatus = '';
            const assistantMessage = item.data.assistantMessage as Message;
            assistantId = assistantMessage.id;
            activeAssistantId = assistantMessage.id;
            messages = [...messages, assistantMessage];
          } else if (item.event === 'response.reasoning.delta') {
            appendTextPart(assistantId, 'reasoning', String(item.data.delta || ''));
          } else if (item.event === 'response.text.delta') {
            appendTextPart(assistantId, 'text', String(item.data.delta || ''));
          } else if (item.event === 'response.tool') {
            updateToolPart(assistantId, item.data);
          } else if (item.event === 'response.image' && item.data.attachmentId) {
            appendImagePart(assistantId, String(item.data.attachmentId));
          } else if (item.event === 'response.context') {
            contextStatus = contextLabel(String(item.data.status || ''));
            if (item.data.status === 'completed') void refreshCheckpoints(activeConversation.id);
          } else if (item.event === 'response.completed') {
            replaceMessage(item.data.message as Message);
            contextStatus = '';
          } else if (item.event === 'response.error') {
            if (item.data.messageRecord) replaceMessage(item.data.messageRecord as Message);
            workspaceError = localizedAPIError(
              String(item.data.code || ''),
              String(item.data.message || t('回答生成失败。', 'Response generation failed.'))
            );
          }
          queueScroll();
        },
        abortController.signal
      );
    } catch (error) {
      const value = errorMessage(error);
      if (assistantId) {
        const restored = await reconcileStreamFailure(activeConversation.id, assistantId);
        if (restored?.status === 'completed') {
          workspaceError = '';
        } else {
          if (value) workspaceError = value;
          if (!restored) {
            messages = messages.map((item) =>
              item.id === assistantId ? { ...item, status: 'interrupted' } : item
            );
          }
        }
      } else if (value) {
        workspaceError = value;
      }
    } finally {
      generating = false;
      abortController = null;
      activeAssistantId = '';
      contextStatus = '';
      await refreshCheckpoints(activeConversation.id);
      queueScroll();
    }
  }

  function titleFrom(value: string): string {
    const line = value.replace(/\s+/g, ' ').trim();
    return Array.from(line).slice(0, 36).join('') || 'New chat';
  }

  function appendTextPart(messageId: string, type: 'text' | 'reasoning', delta: string) {
    if (!messageId || !delta) return;
    messages = messages.map((message) => {
      if (message.id !== messageId) return message;
      const index = message.parts.findIndex((part) => part.type === type);
      let parts: MessagePart[];
      if (index < 0) {
        parts = [...message.parts, { type, text: delta }];
      } else {
        parts = message.parts.map((part, current) =>
          current === index ? { ...part, text: (part.text || '') + delta } : part
        );
      }
      return { ...message, status: 'streaming', parts };
    });
  }

  function updateToolPart(messageId: string, data: Record<string, unknown>) {
    if (!messageId) return;
    messages = messages.map((message) => {
      if (message.id !== messageId) return message;
      const callId = String(data.callId || data.type || 'tool');
      const index = message.parts.findIndex(
        (part) => part.type === 'tool' && String(part.data?.callId || part.data?.type) === callId
      );
      const next: MessagePart = { type: 'tool', data };
      const parts =
        index < 0
          ? [...message.parts, next]
          : message.parts.map((part, current) => (current === index ? next : part));
      return { ...message, parts };
    });
  }

  function appendImagePart(messageId: string, attachmentId: string) {
    messages = messages.map((message) => {
      if (message.id !== messageId) return message;
      if (message.parts.some((part) => part.attachmentId === attachmentId)) return message;
      return { ...message, parts: [...message.parts, { type: 'image', attachmentId }] };
    });
  }

  function replaceMessage(updated: Message) {
    messages = messages.map((message) => (message.id === updated.id ? updated : message));
  }

  function contextLabel(status: string): string {
    if (status === 'started') return t('正在整理较早的对话…', 'Summarizing earlier context…');
    if (status === 'completed') return t('上下文已整理完成', 'Context summary complete');
    if (status === 'skipped') return '';
    return status ? t('正在准备上下文…', 'Preparing context…') : '';
  }

  async function refreshCheckpoints(conversationId: string) {
    try {
      checkpoints = await getContextCheckpoints(conversationId);
    } catch {
      // The next conversation refresh will retry without interrupting the answer.
    }
  }

  function checkpointsAfter(items: ContextCheckpoint[], messageId: string): ContextCheckpoint[] {
    return items
      .filter((checkpoint) => checkpoint.boundaryMessageId === messageId)
      .sort((left, right) => left.createdAt - right.createdAt);
  }

  async function stopGeneration() {
    const controller = abortController;
    if (activeAssistantId) {
      try {
        await cancelResponse(activeAssistantId);
      } catch {
        // Disconnecting the stream still cancels the upstream request.
      }
    }
    controller?.abort();
  }

  function queueScroll() {
    if (scrollQueued) return;
    scrollQueued = true;
    requestAnimationFrame(() => {
      scrollQueued = false;
      void scrollToBottom(true);
    });
  }

  async function scrollToBottom(smooth: boolean) {
    await tick();
    scrollElement?.scrollTo({
      top: scrollElement.scrollHeight,
      behavior: smooth ? 'smooth' : 'auto'
    });
  }

  function applyTheme() {
    resolvedTheme =
      theme === 'system'
        ? matchMedia('(prefers-color-scheme: dark)').matches
          ? 'dark'
          : 'light'
        : theme;
    document.documentElement.dataset.theme = resolvedTheme;
  }

  function toggleTheme() {
    theme = theme === 'system' ? 'dark' : theme === 'dark' ? 'light' : 'system';
    applyTheme();
    localStorage.setItem('personal-chat-theme', theme);
  }

  function themeLabel(): string {
    if (theme === 'system') return t('跟随系统', 'Use system theme');
    return theme === 'dark' ? t('深色模式', 'Dark mode') : t('浅色模式', 'Light mode');
  }

  function toggleLocale() {
    setLocale($locale === 'zh-CN' ? 'en' : 'zh-CN');
    profileOpen = false;
  }

  function effortLabel(effort: string): string {
    const labels: Record<string, [string, string]> = {
      none: ['不推理', 'None'],
      minimal: ['极低', 'Minimal'],
      low: ['低', 'Low'],
      medium: ['中', 'Medium'],
      high: ['高', 'High'],
      xhigh: ['极高', 'Extra high'],
      max: ['最高', 'Maximum'],
      ultra: ['超高', 'Ultra']
    };
    const label = labels[effort];
    return `${t('推理', 'Reasoning')} · ${label ? t(label[0], label[1]) : effort}`;
  }

  function modelCapabilityLabel(model: Model): string {
    if (!model.capabilitiesComplete) return t('能力待确认', 'Capabilities pending');
    const capabilities: string[] = [];
    if (model.inputModalities?.includes('image')) {
      capabilities.push(t('图片输入', 'Image input'));
    }
    if (model.supportsWebSearch) {
      capabilities.push(t('联网', 'Web search'));
    }
    if (model.imageGenerationMode) {
      capabilities.push(t('图片生成', 'Image generation'));
    }
    if (model.reasoningEfforts?.length) {
      capabilities.push(
        `${t('推理', 'Reasoning')}: ${model.reasoningEfforts.join('/')}`
      );
    }
    return capabilities.join(' · ') || t('文本聊天', 'Text chat');
  }
</script>

{#if phase === 'boot'}
  <main class="boot-screen">
    <div class="brand-mark large"><Icon name="sparkles" size={25} /></div>
    <div class="boot-bar"><i></i></div>
  </main>
{:else if phase === 'login'}
  <main class="login-screen">
    <button class="login-locale" on:click={toggleLocale}>
      <Icon name="globe" size={16} />{$locale === 'zh-CN' ? 'English' : '中文'}
    </button>
    <div class="login-ambient ambient-one"></div>
    <div class="login-ambient ambient-two"></div>
    <section class="login-card">
      <div class="brand-mark"><Icon name="sparkles" size={21} /></div>
      <h1>{t('欢迎回来', 'Welcome back')}</h1>
      <p class="login-subtitle">{t('登录你的私人 AI 空间', 'Sign in to your private AI space')}</p>
      <form on:submit|preventDefault={submitLogin}>
        <label>
          <span>{t('用户名', 'Username')}</span>
          <input
            name="username"
            autocomplete="username"
            required
            placeholder={t('输入用户名', 'Enter your username')}
          />
        </label>
        <label>
          <span>{t('密码', 'Password')}</span>
          <input
            name="password"
            type="password"
            autocomplete="current-password"
            required
            placeholder={t('输入密码', 'Enter your password')}
          />
        </label>
        {#if loginError}<div class="form-error" role="alert">{loginError}</div>{/if}
        <button class="primary-button" type="submit">
          <span>{t('登录', 'Sign in')}</span>
          <Icon name="send" size={17} />
        </button>
      </form>
      <p class="login-note">
        {t('账户由服务器管理员创建 · 不开放注册', 'Accounts are created by the server administrator · Registration is closed')}
      </p>
    </section>
    <footer>Personal Chat · Based on Open WebUI</footer>
  </main>
{:else}
  <div class="app-shell">
    {#if sidebarOpen}
      <button
        class="sidebar-scrim"
        aria-label={t('关闭侧边栏', 'Close sidebar')}
        on:click={() => (sidebarOpen = false)}
      ></button>
    {/if}
    <aside class:open={sidebarOpen} class="sidebar">
      <div class="sidebar-top">
        <div class="sidebar-brand">
          <div class="brand-mark small"><Icon name="sparkles" size={15} /></div>
          <span>Personal Chat</span>
        </div>
        <button
          class="icon-button mobile-close"
          aria-label={t('关闭', 'Close')}
          on:click={() => (sidebarOpen = false)}
        >
          <Icon name="close" size={20} />
        </button>
      </div>
      <button class="new-chat" on:click={newConversation} disabled={generating || !selectableModels.length}>
        <span class="plus"><Icon name="plus" size={18} /></span>
        <span>{t('新对话', 'New chat')}</span>
        <kbd>⌘ K</kbd>
      </button>

      <div class="history-label">
        {showArchived ? t('已归档', 'Archived') : t('对话记录', 'Chats')}
      </div>
      <nav class="conversation-list" aria-label={t('对话记录', 'Chats')}>
        {#each conversations as conversation (conversation.id)}
          <div
            class:active={conversation.id === activeConversationId}
            class="conversation-item"
          >
            {#if editingTitleId === conversation.id}
              <span class="conversation-icon"><Icon name="chat" size={16} /></span>
              <input
                class="rename-input"
                bind:value={editingTitle}
                on:blur={() => finishRename(conversation)}
                on:keydown={(event) => {
                  if (event.key === 'Enter') (event.currentTarget as HTMLInputElement).blur();
                  if (event.key === 'Escape') editingTitleId = '';
                }}
              />
            {:else}
              <button
                class="conversation-main"
                type="button"
                on:click={() => openConversation(conversation.id)}
                aria-current={conversation.id === activeConversationId ? 'page' : undefined}
              >
                <span class="conversation-icon"><Icon name="chat" size={16} /></span>
                <span class="conversation-title">{conversation.title}</span>
              </button>
            {/if}
            <span class="conversation-actions">
              <button
                type="button"
                class="mini-action"
                aria-label={showArchived ? t('恢复', 'Restore') : t('归档', 'Archive')}
                title={showArchived ? t('恢复', 'Restore') : t('归档', 'Archive')}
                on:click={(event) => setArchived(event, conversation, !showArchived)}
              ><Icon name={showArchived ? 'restore' : 'archive'} size={15} /></button>
              <button
                type="button"
                class="mini-action"
                aria-label={t('重命名', 'Rename')}
                title={t('重命名', 'Rename')}
                on:click={(event) => beginRename(event, conversation)}
              ><Icon name="edit" size={15} /></button>
              <button
                type="button"
                class="mini-action danger"
                aria-label={t('删除', 'Delete')}
                title={t('删除', 'Delete')}
                on:click={(event) => removeConversation(event, conversation)}
              ><Icon name="trash" size={15} /></button>
            </span>
          </div>
        {/each}
        {#if conversations.length === 0}
          <p class="no-history">
            {showArchived
              ? t('没有已归档的对话', 'No archived chats')
              : t('开始第一段对话吧', 'Start your first chat')}
          </p>
        {/if}
      </nav>

      <div class="sidebar-footer">
        <button class="profile-button" on:click={() => (profileOpen = !profileOpen)}>
          <span class="profile-avatar">{user?.displayName?.slice(0, 1).toUpperCase()}</span>
          <span class="profile-copy">
            <strong>{user?.displayName}</strong>
            <small>@{user?.username}</small>
          </span>
          <Icon name="more" size={18} />
        </button>
        {#if profileOpen}
          <div class="profile-menu">
            <button on:click={toggleTheme}>
              <Icon
                name={theme === 'system' ? 'theme' : resolvedTheme === 'dark' ? 'sun' : 'moon'}
                size={17}
              />
              {themeLabel()}
            </button>
            <button on:click={toggleArchiveView}>
              <Icon name={showArchived ? 'chat' : 'archive'} size={17} />
              {showArchived ? t('返回对话记录', 'Back to chats') : t('已归档对话', 'Archived chats')}
            </button>
            <button on:click={toggleLocale}>
              <Icon name="globe" size={17} />{$locale === 'zh-CN' ? 'English' : '中文'}
            </button>
            <button on:click={refreshModels}><Icon name="refresh" size={17} />{t('刷新模型目录', 'Refresh model catalog')}</button>
            <button on:click={() => openDialog('security')}><Icon name="shield" size={17} />{t('账户与安全', 'Account & security')}</button>
            <button on:click={() => openDialog('about')}><Icon name="info" size={17} />{t('关于', 'About')}</button>
            <button class="logout-button" on:click={doLogout}><Icon name="logout" size={17} />{t('退出登录', 'Sign out')}</button>
          </div>
        {/if}
      </div>
    </aside>

    <main class="chat-panel">
      <header class="chat-header">
        <button
          class="icon-button menu-button"
          aria-label={t('打开侧边栏', 'Open sidebar')}
          on:click={() => (sidebarOpen = true)}
        >
          <Icon name="menu" size={20} />
        </button>
        <div class="selectors">
          {#if modelPickerOpen}
            <button
              class="model-picker-scrim"
              aria-label={t('关闭模型列表', 'Close model list')}
              on:click={() => (modelPickerOpen = false)}
            ></button>
          {/if}
          <div class="model-picker">
            <button
              type="button"
              class="model-picker-trigger"
              aria-haspopup="listbox"
              aria-expanded={modelPickerOpen}
              on:click={toggleModelPicker}
              disabled={generating || showArchived || !selectableModels.length}
            >
              <span>{activeModel?.name || activeConversation?.model || draftModel || t('选择模型', 'Select model')}</span>
              <Icon name="chevron-down" size={15} />
            </button>
            {#if modelPickerOpen}
              <div class="model-picker-panel">
                <label class="model-search">
                  <span class="sr-only">{t('搜索模型', 'Search models')}</span>
                  <span class="model-search-icon"><Icon name="search" size={17} /></span>
                  <input
                    bind:value={modelSearch}
                    placeholder={t('按名称或模型 ID 搜索', 'Search by name or model ID')}
                    on:keydown={(event) => {
                      if (event.key === 'Enter' && filteredModels[0]) {
                        event.preventDefault();
                        void selectModel(filteredModels[0].id);
                      }
                    }}
                  />
                </label>
                <div class="model-options" role="listbox" aria-label={t('聊天模型', 'Chat models')}>
                  {#if activeConversation && !activeModel}
                    <div class="model-unavailable">
                      <strong>{activeConversation.model}</strong>
                      <span>{t('当前不可用；刷新目录后再试', 'Currently unavailable; refresh the catalog and try again')}</span>
                    </div>
                  {/if}
                  {#each filteredModels as model}
                    <button
                      type="button"
                      role="option"
                      aria-selected={model.id === (activeConversation?.model || draftModel)}
                      class:selected={model.id === (activeConversation?.model || draftModel)}
                      on:click={() => selectModel(model.id)}
                    >
                      <span class="model-option-copy">
                        <strong>{model.name}</strong>
                        <code>{model.id}</code>
                        <small>{modelCapabilityLabel(model)}</small>
                      </span>
                      {#if model.id === (activeConversation?.model || draftModel)}
                        <span class="model-check"><Icon name="check" size={17} /></span>
                      {/if}
                    </button>
                  {/each}
                  {#if filteredModels.length === 0}
                    <p>{t('没有匹配的模型', 'No matching models')}</p>
                  {/if}
                </div>
              </div>
            {/if}
          </div>
          {#if activeModel && (activeModel.reasoningEfforts?.length || activeConversation)}
            <label
              class="select-control effort-select"
              title={t(
                '更高推理强度通常响应更慢并消耗更多额度；它不是原始思维链开关。',
                'Higher reasoning effort is usually slower and uses more quota; it is not a raw chain-of-thought switch.'
              )}
            >
              <span class="sr-only">{t('推理强度', 'Reasoning effort')}</span>
              <span class="effort-icon"><Icon name="sparkles" size={12} /></span>
              <select
                value={selectedReasoningEffort}
                on:change={changeEffort}
                disabled={generating || showArchived}
              >
                {#each effortOptions as effort}
                  <option value={effort}>
                    {effort === 'auto' ? t('自动推理', 'Reasoning · Auto') : effortLabel(effort)}
                  </option>
                {/each}
              </select>
              <span class="select-chevron"><Icon name="chevron-down" size={13} /></span>
            </label>
          {/if}
        </div>
        <button
          class="icon-button theme-header"
          aria-label={t('切换主题', 'Change theme')}
          on:click={toggleTheme}
        >
          <Icon
            name={theme === 'system' ? 'theme' : resolvedTheme === 'dark' ? 'sun' : 'moon'}
            size={19}
          />
        </button>
      </header>

      <div
        class="messages-scroll"
        bind:this={scrollElement}
        role="log"
        aria-live="polite"
        aria-relevant="additions text"
        aria-label={t('对话内容', 'Conversation')}
      >
        {#if loadingMessages}
          <div class="message-skeleton">
            <i></i><i></i><i></i>
          </div>
        {:else if messages.length === 0}
          <section class="welcome">
            <div class="welcome-glyph"><Icon name="sparkles" size={23} /></div>
            <h2>{t('今天想聊点什么？', 'What would you like to talk about?')}</h2>
            <p>
              {t(
                '我可以帮你写作、分析、搜索网页，也能理解和生成图片。',
                'I can help you write, analyze, search the web, and understand or generate images.'
              )}
            </p>
            <div class="suggestion-grid">
              <button on:click={() => {
                text = t('帮我规划一个轻松的周末约会安排', 'Help me plan a relaxed weekend date');
                resizeComposer();
                textareaElement?.focus();
              }}>
                <span><Icon name="plan" size={18} /></span>
                <strong>{t('规划灵感', 'Plan something')}</strong>
                <small>{t('安排一次轻松的周末约会', 'Plan a relaxed weekend date')}</small>
              </button>
              <button on:click={() => {
                text = t(
                  '搜索并总结今天值得关注的科技新闻',
                  'Search for and summarize today’s notable tech news'
                );
                resizeComposer();
                textareaElement?.focus();
              }}>
                <span><Icon name="search" size={18} /></span>
                <strong>{t('联网搜索', 'Web search')}</strong>
                <small>{t('总结今天的科技新闻', 'Summarize today’s tech news')}</small>
              </button>
              <button
                title={!activeModel?.imageGenerationMode
                  ? t('当前模型不支持图片生成', 'The current model does not support image generation')
                  : t('进入图片生成模式', 'Enter image generation mode')}
                on:click={prepareImagePrompt}
              >
                <span><Icon name="image-plus" size={18} /></span>
                <strong>{t('生成图片', 'Generate an image')}</strong>
                <small>{t('创作一张梦幻风格插画', 'Create a dreamlike illustration')}</small>
              </button>
            </div>
          </section>
        {:else}
          <div class="message-column">
            {#each messages as message (message.id)}
              <MessageView
                {message}
                locale={$locale}
                canRegenerate={message.role === 'assistant' && message.id === messages.at(-1)?.id && !generating}
                on:regenerate={(event) => regenerate(event.detail.message)}
              />
              {#each checkpointsAfter(checkpoints, message.id) as checkpoint (checkpoint.id)}
                <details class="context-checkpoint">
                  <summary>
                    <span><Icon name="sparkles" size={13} /></span>
                    {t('较早上下文已摘要', 'Earlier context summarized')}
                    <small>{checkpoint.estimatedTokensBefore.toLocaleString()} → {checkpoint.estimatedTokensAfter.toLocaleString()} tokens</small>
                  </summary>
                  <div>
                    <p>
                      {t(
                        '原始消息与附件仍完整保留；以下内容只用于后续模型上下文。',
                        'Original messages and attachments remain intact; this summary is used only as later model context.'
                      )}
                    </p>
                    <pre>{checkpoint.summaryText}</pre>
                  </div>
                </details>
              {/each}
            {/each}
          </div>
        {/if}
      </div>

      <div class="composer-zone">
        {#if contextStatus}
          <div class="context-status" role="status">
            <span><Icon name="sparkles" size={14} /></span>{contextStatus}
          </div>
        {/if}
        {#if checkpoints.length >= 2}
          <div class="context-advice">
            {t(
              '长对话已多次摘要；需要精确引用旧内容时，请重新附上原文或图片。',
              'This long chat has been summarized more than once. Reattach the original text or image when exact details matter.'
            )}
          </div>
        {/if}
        {#if workspaceError}
          <div class="workspace-error" role="alert">
            <Icon name="alert" size={16} />
            <span>{workspaceError}</span>
            <button aria-label={t('关闭', 'Close')} on:click={() => (workspaceError = '')}>
              <Icon name="close" size={15} />
            </button>
          </div>
        {/if}
        <div class:busy={generating} class:image-mode={generateImage} class="composer">
          {#if uploads.length}
            <div class="upload-strip">
              {#each uploads as attachment (attachment.id)}
                <div class="upload-preview">
                  <img src={attachment.url} alt={attachment.originalName || t('上传图片', 'Uploaded image')} />
                  <button
                    aria-label={t('移除图片', 'Remove image')}
                    on:click={() => removeUpload(attachment)}
                  ><Icon name="close" size={14} /></button>
                </div>
              {/each}
            </div>
          {/if}
          <textarea
            bind:this={textareaElement}
            bind:value={text}
            on:input={resizeComposer}
            on:keydown={composerKeydown}
            placeholder={showArchived
              ? t('已归档对话为只读', 'Archived chats are read-only')
              : generateImage
                ? t('描述你想生成的图片', 'Describe the image you want to create')
                : t('给 AI 发送消息', 'Message AI')}
            rows="1"
            aria-label={generateImage
              ? t('图片生成描述', 'Image generation prompt')
              : t('聊天消息', 'Chat message')}
            disabled={generating || showArchived}
          ></textarea>
          <div class="composer-toolbar">
            <div>
              <input
                class="hidden-file"
                bind:this={fileElement}
                type="file"
                accept="image/png,image/jpeg,image/webp"
                multiple
                on:change={chooseFiles}
              />
              <button
                class="toolbar-button"
                title={activeModel?.capabilitiesComplete &&
                !activeModel.inputModalities?.includes('image')
                  ? t('当前模型不支持图片输入', 'The current model does not support image input')
                  : t('上传图片（单张不超过 12 MiB）', 'Upload images (up to 12 MiB each)')}
                on:click={() => fileElement?.click()}
                disabled={generating ||
                  showArchived ||
                  uploading ||
                  generateImage ||
                  uploads.length >= 4 ||
                  (activeModel?.capabilitiesComplete &&
                    !activeModel.inputModalities?.includes('image'))}
              >
                {#if uploading}
                  <span class="toolbar-spinner" aria-hidden="true"></span>
                {:else}
                  <Icon name="upload" size={15} />
                {/if}
                <span>{t('上传图片', 'Upload image')}</span>
              </button>
              <button
                class="toolbar-button image-mode-button"
                class:active={generateImage}
                aria-pressed={generateImage}
                title={!activeModel?.imageGenerationMode
                  ? t('当前模型不支持图片生成', 'The current model does not support image generation')
                  : generateImage
                    ? t('退出图片生成模式', 'Exit image generation mode')
                    : t(
                        '生成图片（质量由服务端自动选择）',
                        'Generate an image (server selects quality automatically)'
                      )}
                on:click={toggleImageGeneration}
                disabled={generating ||
                  showArchived ||
                  !activeModel?.imageGenerationMode ||
                  uploads.length > 0}
              >
                <Icon name="image-plus" size={15} />
                <span>
                  {generateImage
                    ? t('正在绘图', 'Image mode')
                    : t('生成图片', 'Generate image')}
                </span>
              </button>
              {#if activeModel?.supportsWebSearch}
                <span class="capability-pill"><i></i>{t('可联网', 'Web enabled')}</span>
              {/if}
            </div>
            {#if generating}
              <button
                class="send-button stop"
                aria-label={t('停止生成', 'Stop generating')}
                on:click={stopGeneration}
              ><Icon name="stop" size={17} /></button>
            {:else}
              <button
                class="send-button"
                aria-label={t('发送', 'Send')}
                on:click={send}
                disabled={showArchived || uploading || (!text.trim() && uploads.length === 0)}
              ><Icon name="send" size={18} /></button>
            {/if}
          </div>
        </div>
        <p class="disclaimer">
          {t('AI 可能会犯错，请核对重要信息。', 'AI can make mistakes. Check important information.')}
        </p>
      </div>
    </main>
  </div>

  {#if dialog}
    <div class="modal-layer">
      <button
        class="modal-backdrop"
        aria-label={t('关闭对话框', 'Close dialog')}
        on:click={() => (dialog = '')}
      ></button>
      <div
        class="account-dialog"
        bind:this={dialogElement}
        role="dialog"
        aria-modal="true"
        aria-labelledby="dialog-title"
        tabindex="-1"
      >
        <button
          class="dialog-close"
          aria-label={t('关闭', 'Close')}
          on:click={() => (dialog = '')}
        ><Icon name="close" size={20} /></button>
        {#if dialog === 'security'}
          <div class="dialog-icon"><Icon name="lock" size={23} /></div>
          <h2 id="dialog-title">{t('账户与安全', 'Account & security')}</h2>
          <p class="dialog-lead">
            {t(
              '修改密码会注销这个账户在所有设备上的会话。新密码至少需要 12 个字符。',
              'Changing your password signs this account out on every device. The new password must be at least 12 characters.'
            )}
          </p>
          <form class="password-form" on:submit|preventDefault={submitPasswordChange}>
            <label>
              <span>{t('当前密码', 'Current password')}</span>
              <input
                name="currentPassword"
                type="password"
                autocomplete="current-password"
                required
                disabled={accountPending}
              />
            </label>
            <label>
              <span>{t('新密码', 'New password')}</span>
              <input
                name="newPassword"
                type="password"
                autocomplete="new-password"
                minlength="12"
                required
                disabled={accountPending}
              />
            </label>
            <label>
              <span>{t('确认新密码', 'Confirm new password')}</span>
              <input
                name="confirmation"
                type="password"
                autocomplete="new-password"
                minlength="12"
                required
                disabled={accountPending}
              />
            </label>
            {#if accountError}<div class="account-error" role="alert">{accountError}</div>{/if}
            <button class="dialog-primary" type="submit" disabled={accountPending}>
              {accountPending
                ? t('正在更新…', 'Updating…')
                : t('修改密码并退出', 'Change password and sign out')}
            </button>
          </form>
          <div class="session-divider"><span>{t('或者', 'or')}</span></div>
          <button class="logout-everywhere" on:click={doLogoutAll} disabled={accountPending}>
            {t('注销此账户的全部会话', 'Sign this account out everywhere')}
          </button>
        {:else}
          <div class="dialog-icon"><Icon name="sparkles" size={23} /></div>
          <h2 id="dialog-title">Personal Chat</h2>
          <p class="dialog-lead">
            {t(
              '为两名受邀用户精简的私人 AI 聊天界面，由 Open WebUI 的设计与代码基础衍生。',
              'A streamlined private AI chat for two invited users, derived from the Open WebUI design and codebase.'
            )}
          </p>
          <div class="about-grid">
            <div><span>{t('上游项目', 'Upstream')}</span><strong>Open WebUI</strong></div>
            <div><span>{t('后端', 'Backend')}</span><strong>Go + SQLite</strong></div>
            <div><span>{t('AI 接口', 'AI interface')}</span><strong>CPA / OpenAI Responses</strong></div>
            <div>
              <span>{t('注册', 'Registration')}</span>
              <strong>{t('关闭，仅管理员建号', 'Closed; admin-created accounts only')}</strong>
            </div>
          </div>
          <div class="privacy-note">
            <strong>{t('隐私边界', 'Privacy boundary')}</strong>
            <p>
              {t(
                '对话会保存在本服务器，并发送给配置的 CPA 上游完成推理；它不是端到端加密服务。工具面板只展示经过白名单过滤的过程数据。',
                'Chats are stored on this server and sent to the configured CPA upstream for inference; this is not an end-to-end encrypted service. Tool panels show only allowlisted process data.'
              )}
            </p>
          </div>
          <p class="license-copy">
            {t(
              '保留 Open WebUI 原始许可证与归属文件；本项目不代表其官方版本。',
              'The original Open WebUI license and attribution files are retained; this is not an official Open WebUI release.'
            )}
          </p>
        {/if}
      </div>
    </div>
  {/if}
{/if}

<svelte:window
  on:keydown={(event) => {
    if (event.key === 'Escape') {
      if (modelPickerOpen) {
        modelPickerOpen = false;
        return;
      }
      if (dialog) {
        dialog = '';
        return;
      }
    }
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k' && phase === 'ready') {
      event.preventDefault();
      void newConversation();
    }
  }}
/>
