<script lang="ts">
  import { onMount, tick } from 'svelte';
  import {
    APIError,
    cancelResponse,
    changePassword,
    createConversation,
    deleteAttachment,
    deleteConversation,
    editResponse,
    getConversations,
    getContextCheckpoints,
    getMessages,
    getModels,
    getResponse,
    getSession,
    getStorageStatus,
    getUsage,
    getWorkbench,
    login,
    logout,
    logoutAll,
    regenerateResponse,
    searchConversations,
    streamGuidanceResponse,
    streamResponse,
    updateConversation,
    updateConversationWithMeta,
    updateWorkbench,
    uploadAttachment
  } from './lib/api';
  import { locale, setLocale, translate } from './lib/i18n';
  import Icon from './lib/Icon.svelte';
  import { messageText } from './lib/messages';
  import MessageView from './lib/MessageView.svelte';
  import Onboarding from './lib/Onboarding.svelte';
  import ProgressiveSummarySettings from './lib/ProgressiveSummarySettings.svelte';
  import SpeechAdminSettings from './lib/SpeechAdminSettings.svelte';
  import SpeechPlayer from './lib/SpeechPlayer.svelte';
  import SpeechSettings from './lib/SpeechSettings.svelte';
  import UpdateAnnouncement from './lib/UpdateAnnouncement.svelte';
  import {
    initializeSpeech,
    refreshSpeechPreference,
    resetSpeech,
    speechController,
    speechDeviceAuthorization,
    speechPlayerState,
    speechPreference
  } from './lib/speech';
  import type {
    Attachment,
    Conversation,
    ConversationSearchResult,
    ContextCheckpoint,
    GuidanceSubmission,
    Message,
    MessagePart,
    Model,
    StorageStatus,
    StreamEvent,
    UsageRow,
    User,
    WorkbenchSetting
  } from './lib/types';

  type Phase = 'boot' | 'login' | 'ready';
  type GenerationStage =
    | 'sending'
    | 'queued'
    | 'preparing_context'
    | 'waiting_for_model'
    | 'reasoning'
    | 'searching'
    | 'generating_image'
    | 'answering'
    | 'background';
  type SuggestionIcon = 'plan' | 'search' | 'image-plus' | 'sparkles' | 'upload';
  type Suggestion = {
    id: string;
    icon: SuggestionIcon;
    chineseTitle: string;
    englishTitle: string;
    chinesePrompt: string;
    englishPrompt: string;
    mode?: 'image';
  };
  type FontSize = 'compact' | 'standard' | 'large';
  type ModelMode = 'fast' | 'balanced' | 'expert';
  type ModelModeInfo = {
    key: ModelMode;
    chineseName: string;
    englishName: string;
    technicalName: 'Luna' | 'Terra' | 'Sol';
    chineseDescription: string;
    englishDescription: string;
  };

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
  let loginUsername = localStorage.getItem('personal-chat-remember-username') || '';
  let loginPassword = '';
  let rememberPassword = localStorage.getItem('personal-chat-remember-password') === 'true';
  let contextStatus = '';
  let sidebarOpen = false;
  let profileOpen = false;
  let dialog:
    | ''
    | 'appearance'
    | 'security'
    | 'service'
    | 'speech'
    | 'speech-admin'
    | 'usage'
    | 'about' = '';
  let accountError = '';
  let accountPending = false;
  let showArchived = false;
  let abortController: AbortController | null = null;
  let activeAssistantId = '';
  let activeResponseCancelId = '';
  let scrollElement: HTMLDivElement;
  let textareaElement: HTMLTextAreaElement;
  let fileElement: HTMLInputElement;
  let dialogElement: HTMLDivElement;
  let scrollQueued = false;
  let scrollStateQueued = false;
  let followStream = true;
  let showJumpToLatest = false;
  let editingTitleId = '';
  let editingTitle = '';
  let modelPickerOpen = false;
  let effortPickerOpen = false;
  let storageStatus: StorageStatus | null = null;
  let workbenchSetting: WorkbenchSetting | null = null;
  let restaurantGuidanceEnabled = false;
  let workbenchPickerOpen = false;
  let workbenchUpdating = false;
  let creatingConversation = false;
  let generationStage: GenerationStage | '' = '';
  let generationStartedAt = 0;
  let generationNow = Date.now();
  let generationTimer: number | undefined;
  let responseWatchVersion = 0;
  let onboardingOpen = false;
  let updateAnnouncementOpen = false;
  let visibleSuggestions: Suggestion[] = [];
  let searchQuery = '';
  let searchResults: ConversationSearchResult[] = [];
  let searching = false;
  let searchError = '';
  let searchTimer: number | undefined;
  let searchVersion = 0;
  let editingMessageId = '';
  let editingMessageText = '';
  let editTextareaElement: HTMLTextAreaElement | null = null;
  let usageRows: UsageRow[] = [];
  let usageLoading = false;
  let usageError = '';
  let dragDepth = 0;
  let draftStorageKey = '';
  let draftSaveTimer: number | undefined;
  const fontSizeChoices = [
    {
      value: 'compact',
      chinese: '较小',
      english: 'Small',
      chineseDescription: '界面更紧凑，正文仍保持清晰',
      englishDescription: 'A tighter interface with readable body text'
    },
    {
      value: 'standard',
      chinese: '标准',
      english: 'Default',
      chineseDescription: '适合大多数屏幕',
      englishDescription: 'Comfortable on most screens'
    },
    {
      value: 'large',
      chinese: '较大',
      english: 'Large',
      chineseDescription: '文字更醒目，页面会自动重排',
      englishDescription: 'Larger text with responsive reflow'
    }
  ] as const;
  const reasoningChoices = [
    {
      value: 'medium',
      chinese: '低',
      english: 'Low',
      chineseDescription: '适合日常聊天，响应更快',
      englishDescription: 'Faster for everyday conversations'
    },
    {
      value: 'high',
      chinese: '中',
      english: 'Medium',
      chineseDescription: '默认档位，质量与速度均衡',
      englishDescription: 'Default balance of quality and speed'
    },
    {
      value: 'max',
      chinese: '高',
      english: 'High',
      chineseDescription: '适合复杂任务，可能等待更久',
      englishDescription: 'For complex tasks; may take longer'
    }
  ] as const;
  const suggestionPool: Suggestion[] = [
    {
      id: 'weekend-date', icon: 'plan',
      chineseTitle: '周末约会', englishTitle: 'Weekend date',
      chinesePrompt: '帮我规划一个轻松、有一点惊喜感的周末约会安排',
      englishPrompt: 'Plan a relaxed weekend date with one small surprise'
    },
    {
      id: 'city-trip', icon: 'plan',
      chineseTitle: '城市短途旅行', englishTitle: 'City break',
      chinesePrompt: '为我们规划一次两天一夜的城市短途旅行，并给出时间表和预算',
      englishPrompt: 'Plan a two-day city break with a schedule and budget'
    },
    {
      id: 'weekly-meals', icon: 'plan',
      chineseTitle: '一周菜单', englishTitle: 'Weekly meals',
      chinesePrompt: '设计一份简单好做的一周晚餐菜单，并整理采购清单',
      englishPrompt: 'Create an easy weekly dinner plan and a shopping list'
    },
    {
      id: 'study-plan', icon: 'plan',
      chineseTitle: '学习计划', englishTitle: 'Study plan',
      chinesePrompt: '把我想学的内容拆成一个循序渐进的四周学习计划',
      englishPrompt: 'Turn what I want to learn into a progressive four-week study plan'
    },
    {
      id: 'fitness-routine', icon: 'plan',
      chineseTitle: '轻量运动计划', englishTitle: 'Gentle fitness plan',
      chinesePrompt: '帮我制定一份适合初学者、每次三十分钟的居家运动计划',
      englishPrompt: 'Create a beginner-friendly 30-minute home workout plan'
    },
    {
      id: 'decision-matrix', icon: 'sparkles',
      chineseTitle: '帮我做决定', englishTitle: 'Compare options',
      chinesePrompt: '我有几个选择，请先问清楚需求，再用表格帮我权衡利弊',
      englishPrompt: 'Ask about my needs, then compare my options in a decision table'
    },
    {
      id: 'message-polish', icon: 'sparkles',
      chineseTitle: '润色一段话', englishTitle: 'Polish a message',
      chinesePrompt: '帮我把下面这段话改得自然、真诚，不要显得过于正式：',
      englishPrompt: 'Rewrite this message so it sounds natural and sincere, not too formal:'
    },
    {
      id: 'email-draft', icon: 'sparkles',
      chineseTitle: '起草邮件', englishTitle: 'Draft an email',
      chinesePrompt: '帮我写一封简洁礼貌的邮件，先向我确认收件人、目的和语气',
      englishPrompt: 'Draft a concise, polite email after asking about the recipient, goal, and tone'
    },
    {
      id: 'story-outline', icon: 'sparkles',
      chineseTitle: '故事大纲', englishTitle: 'Story outline',
      chinesePrompt: '根据我给出的主题，设计人物、冲突和三幕式故事大纲',
      englishPrompt: 'Create characters, conflict, and a three-act outline from my theme'
    },
    {
      id: 'social-caption', icon: 'sparkles',
      chineseTitle: '社交文案', englishTitle: 'Social caption',
      chinesePrompt: '为我写三种不同语气的社交平台文案，避免夸张和网络套话',
      englishPrompt: 'Write three social captions in different tones without hype or clichés'
    },
    {
      id: 'translate-natural', icon: 'sparkles',
      chineseTitle: '自然翻译', englishTitle: 'Natural translation',
      chinesePrompt: '把我接下来提供的内容翻译得自然，并解释容易误译的表达',
      englishPrompt: 'Translate my next text naturally and explain phrases that are easy to mistranslate'
    },
    {
      id: 'explain-simple', icon: 'sparkles',
      chineseTitle: '通俗解释', englishTitle: 'Explain simply',
      chinesePrompt: '用生活中的比喻解释我接下来提出的概念，再给一个具体例子',
      englishPrompt: 'Explain my next concept with an everyday analogy and a concrete example'
    },
    {
      id: 'meeting-summary', icon: 'sparkles',
      chineseTitle: '整理会议记录', englishTitle: 'Meeting notes',
      chinesePrompt: '把我粘贴的会议记录整理成结论、待办事项、负责人和截止时间',
      englishPrompt: 'Turn my meeting notes into decisions, actions, owners, and deadlines'
    },
    {
      id: 'long-summary', icon: 'sparkles',
      chineseTitle: '总结长文', englishTitle: 'Summarize a document',
      chinesePrompt: '总结我接下来提供的长文，分为核心观点、依据和仍需确认的问题',
      englishPrompt: 'Summarize my document into key ideas, evidence, and open questions'
    },
    {
      id: 'brainstorm', icon: 'sparkles',
      chineseTitle: '一起头脑风暴', englishTitle: 'Brainstorm ideas',
      chinesePrompt: '围绕我的目标给出十个不同方向的想法，并挑出最值得尝试的三个',
      englishPrompt: 'Generate ten directions for my goal and select the three most promising'
    },
    {
      id: 'tech-news', icon: 'search',
      chineseTitle: '今日科技动态', englishTitle: 'Today in tech',
      chinesePrompt: '搜索并总结今天值得关注的科技新闻，附上来源和发布日期',
      englishPrompt: 'Search and summarize today’s notable tech news with sources and dates'
    },
    {
      id: 'topic-research', icon: 'search',
      chineseTitle: '快速调研', englishTitle: 'Quick research',
      chinesePrompt: '联网调研我接下来给出的主题，区分事实、观点和仍有争议的部分',
      englishPrompt: 'Research my next topic online, separating facts, opinions, and disputed claims'
    },
    {
      id: 'product-compare', icon: 'search',
      chineseTitle: '产品对比', englishTitle: 'Product comparison',
      chinesePrompt: '搜索并对比我指定的两款产品，重点看真实差异、价格和适用人群',
      englishPrompt: 'Research and compare two products by real differences, price, and ideal users'
    },
    {
      id: 'travel-research', icon: 'search',
      chineseTitle: '旅行攻略', englishTitle: 'Travel research',
      chinesePrompt: '搜索目的地的近期信息，整理交通、住宿区域、注意事项和参考来源',
      englishPrompt: 'Research current destination information: transport, areas to stay, cautions, and sources'
    },
    {
      id: 'fact-check', icon: 'search',
      chineseTitle: '核实一条说法', englishTitle: 'Fact-check a claim',
      chinesePrompt: '联网核实我接下来提供的说法，给出原始来源、时间和可信度判断',
      englishPrompt: 'Fact-check my next claim with primary sources, dates, and a confidence assessment'
    },
    {
      id: 'latest-guide', icon: 'search',
      chineseTitle: '查找最新教程', englishTitle: 'Find a current guide',
      chinesePrompt: '搜索这个主题的最新官方教程，并整理成可以照着执行的步骤：',
      englishPrompt: 'Find the latest official guide for this topic and turn it into actionable steps:'
    },
    {
      id: 'code-review', icon: 'sparkles',
      chineseTitle: '代码审查', englishTitle: 'Review code',
      chinesePrompt: '审查我接下来贴出的代码，优先指出正确性、安全性和可维护性问题',
      englishPrompt: 'Review my code for correctness, security, and maintainability'
    },
    {
      id: 'debug-error', icon: 'sparkles',
      chineseTitle: '排查报错', englishTitle: 'Debug an error',
      chinesePrompt: '帮我排查这个报错：先判断最可能的原因，再给最小验证步骤',
      englishPrompt: 'Debug this error by identifying likely causes and the smallest verification steps'
    },
    {
      id: 'data-table', icon: 'sparkles',
      chineseTitle: '整理成表格', englishTitle: 'Structure as a table',
      chinesePrompt: '把我提供的杂乱信息整理成结构清晰的 Markdown 表格',
      englishPrompt: 'Turn my unstructured information into a clear Markdown table'
    },
    {
      id: 'photo-analysis', icon: 'upload',
      chineseTitle: '看懂一张图片', englishTitle: 'Understand an image',
      chinesePrompt: '我会上传一张图片，请描述关键内容并指出值得注意的细节',
      englishPrompt: 'I will upload an image; describe its key content and notable details'
    },
    {
      id: 'screenshot-help', icon: 'upload',
      chineseTitle: '分析截图问题', englishTitle: 'Analyze a screenshot',
      chinesePrompt: '我会上传一张截图，请定位界面中的问题并给出改进建议',
      englishPrompt: 'I will upload a screenshot; identify UI issues and suggest improvements'
    },
    {
      id: 'dream-illustration', icon: 'image-plus',
      chineseTitle: '梦幻插画', englishTitle: 'Dreamlike illustration',
      chinesePrompt: '生成一张温暖、梦幻、细节丰富的插画，构图自然，光线柔和',
      englishPrompt: 'Create a warm, dreamlike, detailed illustration with natural composition and soft light',
      mode: 'image'
    },
    {
      id: 'poster-design', icon: 'image-plus',
      chineseTitle: '海报概念图', englishTitle: 'Poster concept',
      chinesePrompt: '生成一张简洁现代的海报概念图，留出清晰的标题空间',
      englishPrompt: 'Create a clean modern poster concept with clear space for a title',
      mode: 'image'
    },
    {
      id: 'avatar-design', icon: 'image-plus',
      chineseTitle: '头像创意', englishTitle: 'Avatar concept',
      chinesePrompt: '生成一个辨识度高、背景简洁、适合作为头像的角色形象',
      englishPrompt: 'Create a distinctive character portrait with a simple background for an avatar',
      mode: 'image'
    },
    {
      id: 'room-concept', icon: 'image-plus',
      chineseTitle: '房间氛围图', englishTitle: 'Room concept',
      chinesePrompt: '生成一张舒适明亮的房间氛围图，材质自然，空间真实可落地',
      englishPrompt: 'Create a bright cozy room concept with natural materials and realistic proportions',
      mode: 'image'
    }
  ];
  const restaurantSuggestionPool: Suggestion[] = [
    {
      id: 'restaurant-membership',
      icon: 'plan',
      chineseTitle: '设计会员体系',
      englishTitle: 'Membership program',
      chinesePrompt: '帮我设计饭店的充卡和会员体系',
      englishPrompt: 'Help me design a restaurant prepaid membership program'
    },
    {
      id: 'restaurant-soups',
      icon: 'plan',
      chineseTitle: '特色煨汤',
      englishTitle: 'Signature soups',
      chinesePrompt: '帮我设计 10 款特色煨汤',
      englishPrompt: 'Help me design ten signature slow-simmered soups'
    },
    {
      id: 'restaurant-training',
      icon: 'sparkles',
      chineseTitle: '员工培训',
      englishTitle: 'Staff training',
      chinesePrompt: '帮我做一套饭店新员工服务培训方案',
      englishPrompt: 'Create a service training plan for new restaurant staff'
    },
    {
      id: 'restaurant-menu',
      icon: 'plan',
      chineseTitle: '菜单优化',
      englishTitle: 'Menu improvement',
      chinesePrompt: '我想优化饭店菜单和菜品结构，你帮我规划一下',
      englishPrompt: 'Help me improve my restaurant menu and dish mix'
    },
    {
      id: 'restaurant-social',
      icon: 'sparkles',
      chineseTitle: '短视频宣传',
      englishTitle: 'Social promotion',
      chinesePrompt: '帮我规划饭店接下来一个月的短视频宣传内容',
      englishPrompt: 'Plan one month of short-video promotion for my restaurant'
    }
  ];
  visibleSuggestions = suggestionPool.slice(0, 3);
  type Theme = 'light' | 'dark' | 'system';
  const savedTheme = localStorage.getItem('personal-chat-theme');
  let theme: Theme =
    savedTheme === 'light' || savedTheme === 'dark' || savedTheme === 'system'
      ? savedTheme
      : 'light';
  let resolvedTheme: 'light' | 'dark' = 'light';
  const savedFontSize = localStorage.getItem('personal-chat-font-size');
  let fontSize: FontSize =
    savedFontSize === 'compact' || savedFontSize === 'standard' || savedFontSize === 'large'
      ? savedFontSize
      : 'standard';

  $: activeConversation =
    conversations.find((conversation) => conversation.id === activeConversationId) || null;
  $: activeModel =
    models.find((model) => model.id === (activeConversation?.model || draftModel)) || null;
  $: selectedReasoningEffort =
    activeConversation?.reasoningEffort || draftReasoningEffort;
  $: viewingOtherUser =
    Boolean(
      user?.role === 'admin' &&
        activeConversation?.ownerId &&
        activeConversation.ownerId !== user.id
    );
  $: activeConversationReadOnly = showArchived || viewingOtherUser;
  $: lastUserMessageId = findLastUserMessageId(messages);
  $: dragActive = dragDepth > 0;
  $: selectableModels = visibleModeModels(models);
  $: restaurantWorkbench =
    restaurantGuidanceEnabled &&
    workbenchSetting?.initial === 'restaurant' &&
    workbenchSetting?.effective === 'restaurant';
  $: t = (chinese: string, english: string) => translate($locale, chinese, english);
  $: generationElapsedSeconds = generationStartedAt
    ? Math.max(0, Math.floor((generationNow - generationStartedAt) / 1000))
    : 0;
  $: if (activeAssistantId) {
    const speechMessage = messages.find((message) => message.id === activeAssistantId);
    if (speechMessage) {
      speechController.syncMessage(speechMessage);
      if ($speechPreference?.mode === 'auto') {
        speechController.syncAutomatic(speechMessage);
      }
    }
  }

  onMount(() => {
    setLocale($locale);
    const systemTheme = matchMedia('(prefers-color-scheme: dark)');
    const updateSystemTheme = () => applyTheme();
    systemTheme.addEventListener('change', updateSystemTheme);
    // Flush a pending debounced draft write when the page is being hidden or
    // torn down (mobile tab switches, browser kills).
    const flushDraft = () => persistDraft();
    const flushDraftOnHide = () => {
      if (document.visibilityState === 'hidden') persistDraft();
    };
    window.addEventListener('pagehide', flushDraft);
    document.addEventListener('visibilitychange', flushDraftOnHide);
    applyTheme();
    applyFontSize();
    refreshSuggestions();
    void initialize();
    return () => {
      responseWatchVersion += 1;
      systemTheme.removeEventListener('change', updateSystemTheme);
      window.removeEventListener('pagehide', flushDraft);
      document.removeEventListener('visibilitychange', flushDraftOnHide);
      stopGenerationClock();
      speechController.stop();
    };
  });

  async function initialize() {
    try {
      user = await getSession();
      await Promise.all([loadWorkspace(), initializeSpeech(user.id)]);
      phase = 'ready';
      openEntryPrompts();
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
      not_latest_message: [
        '只能编辑最新一条消息，请刷新后重试。',
        'Only the latest message can be edited. Refresh and try again.'
      ],
      message_not_editable: [
        '这条消息现在无法编辑。',
        'This message cannot be edited right now.'
      ],
      query_too_long: ['搜索内容过长。', 'The search query is too long.'],
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
        '页面连接已中断，回答仍会在服务器后台继续。',
        'The page connection ended. The response will continue on the server.'
      ],
      response_cancelled: ['回答已由你停止。', 'The response was stopped by you.'],
      service_interrupted: [
        '服务重启中断了这次回答；已保留中断前的内容，可手动重试。',
        'A service restart interrupted this response. Saved progress remains available for a manual retry.'
      ],
      response_timeout: [
        '回答运行超过 30 分钟，已由服务器停止。',
        'The response exceeded 30 minutes and was stopped by the server.'
      ],
      response_interrupted: ['回答被中断。', 'The response was interrupted.'],
      persistence_failed: [
        '回答进度无法安全保存，生成已停止。',
        'Response progress could not be saved safely, so generation stopped.'
      ],
      service_stopping: ['服务正在重启，请稍后再试。', 'The service is restarting. Try again shortly.'],
      too_many_requests: ['当前请求较多，请稍后再试。', 'The request queue is full. Try again shortly.'],
      speech_disabled: [
        '管理员暂时关闭了文字转语音服务。',
        'The administrator has disabled text-to-speech.'
      ],
      speech_provider_unavailable: [
        '语音提供商尚未正确配置。',
        'The speech provider is not configured.'
      ],
      speech_session_limit: [
        '当前朗读任务较多，请稍后重试。',
        'Too many read-aloud sessions are active. Try again shortly.'
      ],
      invalid_speech_voice: [
        '所选音色已经不可用，请重新选择。',
        'The selected voice is no longer available.'
      ],
      storage_quota_exceeded: [
        '你的 3 GB 活跃空间已满，请先将对话移入临时留档或删除图片。',
        'Your 3 GB active storage is full. Retain a chat or delete images before continuing.'
      ],
      conversation_limit_reached: [
        '30 个活跃对话都已置顶保护，请先取消置顶或移入临时留档。',
        'All 30 active chats are protected. Unpin or retain one before continuing.'
      ],
      pin_limit_reached: [
        '每名用户最多置顶 10 个对话。',
        'Each user can pin at most 10 chats.'
      ],
      provider_queue_timeout: ['排队等待超时，请重试。', 'The request timed out while queued. Try again.'],
      provider_request_too_large: [
        '当前对话编译后超过 50 MiB，请减少本轮图片或开启新对话。',
        'The compiled conversation exceeds 50 MiB. Remove images from this turn or start a new chat.'
      ],
      stale_guidance: [
        '这张需求卡已提交或已被后续消息替代，请查看最新对话。',
        'This request card was already submitted or superseded. Check the latest message.'
      ],
      invalid_guidance_submission: [
        '本轮选择未能通过校验，请保留选择并重试。',
        'These selections could not be validated. Your draft was kept; try again.'
      ],
      invalid_guidance_output: [
        '交互卡片格式不符合安全限制，可重试或按原问题直接回答。',
        'The interactive card was invalid. Retry it or answer the original request.'
      ],
      guidance_disabled: [
        '餐饮需求引导当前未开启。',
        'Restaurant request guidance is currently disabled.'
      ],
      guidance_state_unavailable: [
        '暂时无法读取餐饮任务状态，请稍后重试。',
        'The restaurant task state is temporarily unavailable.'
      ],
      guidance_bypass_not_allowed: [
        '只有卡片生成失败后才能使用直接回答。',
        'Direct bypass is only available after a card-generation failure.'
      ],
      invalid_workbench: [
        '无法切换到所选工作台。',
        'The selected workbench could not be activated.'
      ],
      internal_error: ['服务器发生内部错误。', 'The server encountered an internal error.']
    };
    const message = messages[code];
    return message ? t(message[0], message[1]) : fallback;
  }

  function effortForModel(model: Model | undefined, requested: string): string {
    if (!model) return 'high';
    const candidates = [
      requested,
      model.defaultReasoningEffort || '',
      'high',
      'medium',
      'max'
    ];
    return candidates.find((effort) => supportsEffort(model, effort)) || 'high';
  }

  function supportsEffort(model: Model | null | undefined, effort: string): boolean {
    if (!model || !model.capabilitiesComplete) return true;
    return Boolean(model.reasoningEfforts?.includes(effort));
  }

  function ownsConversation(conversation: Conversation | null | undefined): boolean {
    return Boolean(conversation && (!conversation.ownerId || conversation.ownerId === user?.id));
  }

  function setDraftModel(modelId: string) {
    draftModel = modelId;
    const model = models.find((item) => item.id === modelId);
    draftReasoningEffort = effortForModel(model, draftReasoningEffort);
    if (!model?.imageGenerationMode) generateImage = false;
  }

  async function loadWorkspace() {
    const [loadedModels, loadedConversations, loadedStorage, loadedWorkbench] = await Promise.all([
      getModels(),
      getConversations(false),
      getStorageStatus().catch(() => null),
      getWorkbench()
    ]);
    models = loadedModels;
    conversations = loadedConversations;
    storageStatus = loadedStorage;
    workbenchSetting = loadedWorkbench.workbench;
    restaurantGuidanceEnabled = loadedWorkbench.guidanceEnabled;
    const availableModes = visibleModeModels(loadedModels);
    const initialDraftModel =
      (user?.preferredModel &&
        availableModes.find((model) => model.id === user?.preferredModel)?.id) ||
      availableModes.find((model) => modelModeInfo(model)?.key === 'expert')?.id ||
      availableModes[0]?.id ||
      '';
    draftModel = initialDraftModel;
    draftReasoningEffort = effortForModel(
      loadedModels.find((model) => model.id === initialDraftModel),
      draftReasoningEffort
    );
    activeConversationId = '';
    messages = [];
    checkpoints = [];
    restoreDraft('');
    uploads = [];
    generateImage = false;
    contextStatus = '';
    showArchived = false;
    followStream = true;
    showJumpToLatest = false;
    localStorage.removeItem('personal-chat-conversation');
    await tick();
    refreshSuggestions();
  }

  async function refreshStorage() {
    try {
      storageStatus = await getStorageStatus();
    } catch {
      // Quota information is advisory; chat state remains usable if it cannot refresh.
    }
  }

  function reloadApplication() {
    profileOpen = false;
    if (generating) {
      workspaceError = t(
        '回答生成完成后再刷新应用。',
        'Wait for the current response to finish before reloading the app.'
      );
      return;
    }
    if (text.trim() || uploads.length > 0) {
      workspaceError = t(
        '输入框还有未发送内容，请先发送或清空后再刷新应用。',
        'The composer contains an unsent draft. Send or clear it before reloading the app.'
      );
      return;
    }
    window.location.reload();
  }

  async function storeBrowserCredential(username: string, password: string) {
    type PasswordCredentialFactory = new (data: {
      id: string;
      password: string;
      name?: string;
    }) => Credential;
    const PasswordCredentialConstructor = (
      window as unknown as { PasswordCredential?: PasswordCredentialFactory }
    ).PasswordCredential;
    if (
      !window.isSecureContext ||
      !PasswordCredentialConstructor ||
      !navigator.credentials?.store
    ) return;
    try {
      await navigator.credentials.store(
        new PasswordCredentialConstructor({ id: username, password, name: username })
      );
    } catch {
      // The browser may require its own password-manager prompt or user setting.
    }
  }

  async function submitLogin(event: SubmitEvent) {
    const form = event.currentTarget as HTMLFormElement;
    const username = loginUsername.trim();
    const password = loginPassword;
    loginError = '';
    form.classList.add('pending');
    try {
      user = await login(username, password);
      if (rememberPassword) {
        localStorage.setItem('personal-chat-remember-password', 'true');
        localStorage.setItem('personal-chat-remember-username', username);
        void storeBrowserCredential(username, password);
      } else {
        localStorage.removeItem('personal-chat-remember-password');
        localStorage.removeItem('personal-chat-remember-username');
      }
      loginPassword = '';
      await Promise.all([loadWorkspace(), initializeSpeech(user.id)]);
      phase = 'ready';
      openEntryPrompts();
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
      responseWatchVersion += 1;
      abortController?.abort();
      generating = false;
      stopGenerationClock();
      activeAssistantId = '';
      activeResponseCancelId = '';
      activeConversationId = '';
      user = null;
      messages = [];
      checkpoints = [];
      conversations = [];
      text = '';
      uploads = [];
      generateImage = false;
      storageStatus = null;
      workbenchSetting = null;
      restaurantGuidanceEnabled = false;
      workbenchPickerOpen = false;
      resetSpeech();
      profileOpen = false;
      onboardingOpen = false;
      updateAnnouncementOpen = false;
      phase = 'login';
    }
  }

  function clearWorkspaceAndShowLogin() {
    responseWatchVersion += 1;
    abortController?.abort();
    generating = false;
    stopGenerationClock();
    activeAssistantId = '';
    activeResponseCancelId = '';
    activeConversationId = '';
    user = null;
    messages = [];
    checkpoints = [];
    conversations = [];
    text = '';
    uploads = [];
    generateImage = false;
    storageStatus = null;
    workbenchSetting = null;
    restaurantGuidanceEnabled = false;
    workbenchPickerOpen = false;
    resetSpeech();
    profileOpen = false;
    dialog = '';
    onboardingOpen = false;
    updateAnnouncementOpen = false;
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

  async function openDialog(
    value: 'appearance' | 'security' | 'service' | 'speech' | 'speech-admin' | 'usage' | 'about'
  ) {
    profileOpen = false;
    accountError = '';
    dialog = value;
    await tick();
    dialogElement?.focus();
  }

  async function authorizeSpeechPlayback() {
    workspaceError = '';
    try {
      await speechController.authorize();
      const speechMessage = messages.find((message) => message.id === activeAssistantId);
      if (speechMessage) speechController.syncAutomatic(speechMessage);
    } catch (error) {
      workspaceError = errorMessage(error);
    }
  }

  async function openConversation(id: string) {
    if (generating || id === activeConversationId && messages.length) {
      sidebarOpen = false;
      return;
    }
    if (id !== activeConversationId) speechController.stop();
    activeConversationId = id;
    const conversation = conversations.find((item) => item.id === id);
    const conversationModel = models.find((item) => item.id === conversation?.model);
    if (!conversationModel?.imageGenerationMode) generateImage = false;
    localStorage.setItem('personal-chat-conversation', id);
    restoreDraft(id);
    editingMessageId = '';
    loadingMessages = true;
    workspaceError = '';
    sidebarOpen = false;
    try {
      [messages, checkpoints] = await Promise.all([getMessages(id), getContextCheckpoints(id)]);
      if (messages.length === 0) refreshSuggestions();
      followStream = true;
      showJumpToLatest = false;
      await scrollToBottom(false);
      resumePersistedResponse(id);
    } catch (error) {
      workspaceError = errorMessage(error);
    } finally {
      loadingMessages = false;
    }
  }

  async function newConversation(): Promise<Conversation | null> {
    if (generating || creatingConversation) return null;
    creatingConversation = true;
    workspaceError = '';
    try {
      const useActiveSettings = ownsConversation(activeConversation);
      const conversation = await createConversation(
        useActiveSettings ? activeConversation?.model || draftModel : draftModel,
        useActiveSettings
          ? activeConversation?.reasoningEffort || draftReasoningEffort
          : draftReasoningEffort
      );
      showArchived = false;
      try {
        conversations = await getConversations(false);
      } catch {
        conversations = [
          conversation,
          ...conversations.filter(
            (item) => item.id !== conversation.id && !item.archivedAt
          )
        ];
      }
      activeConversationId = conversation.id;
      messages = [];
      checkpoints = [];
      contextStatus = '';
      followStream = true;
      showJumpToLatest = false;
      refreshSuggestions();
      localStorage.setItem('personal-chat-conversation', conversation.id);
      clearDraft('');
      draftStorageKey = draftKeyFor(conversation.id);
      sidebarOpen = false;
      await refreshStorage();
      await tick();
      textareaElement?.focus();
      return conversation;
    } catch (error) {
      workspaceError = errorMessage(error);
      return null;
    } finally {
      creatingConversation = false;
    }
  }

  async function startNewChat() {
    if (generating || creatingConversation || !selectableModels.length) return;
    speechController.stop();
    const ownedConversation = ownsConversation(activeConversation) ? activeConversation : null;
    const requestedModel = ownedConversation?.model || draftModel;
    const nextModel =
      selectableModels.find((model) => model.id === requestedModel) ||
      selectableModels.find((model) => modelModeInfo(model)?.key === 'expert') ||
      selectableModels[0];
    const requestedEffort =
      ownedConversation?.reasoningEffort || draftReasoningEffort;

    setDraftModel(nextModel.id);
    draftReasoningEffort = effortForModel(nextModel, requestedEffort);
    activeConversationId = '';
    messages = [];
    checkpoints = [];
    contextStatus = '';
    showArchived = false;
    followStream = true;
    showJumpToLatest = false;
    workbenchPickerOpen = false;
    modelPickerOpen = false;
    effortPickerOpen = false;
    sidebarOpen = false;
    editingMessageId = '';
    localStorage.removeItem('personal-chat-conversation');
    if (text.trim()) {
      // Move (not copy) the typed text into the fresh chat: the old key must
      // be dropped, or the text resurrects in the old chat after being sent
      // here.
      const previousKey = draftStorageKey;
      draftStorageKey = draftKeyFor('');
      persistDraft();
      if (previousKey && previousKey !== draftStorageKey) {
        localStorage.removeItem(previousKey);
      }
    } else {
      restoreDraft('');
    }
    await tick();
    refreshSuggestions();
    textareaElement?.focus();
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
      await refreshStorage();
    } catch (error) {
      workspaceError = errorMessage(error);
    }
  }

  async function togglePinned(event: Event, conversation: Conversation) {
    event.stopPropagation();
    if (generating && conversation.id === activeConversationId) return;
    try {
      const updated = await updateConversation(conversation.id, {
        pinned: !conversation.pinnedAt
      });
      replaceConversation(updated);
      await refreshStorage();
    } catch (error) {
      workspaceError = errorMessage(error);
    }
  }

  async function removeConversation(event: Event, conversation: Conversation) {
    event.stopPropagation();
    if (generating && conversation.id === activeConversationId) return;
    if (!confirm(t(
      `永久删除“${conversation.title}”及其全部消息和图片？此操作无法恢复。`,
      `Permanently delete “${conversation.title}” and all of its messages and images? This cannot be undone.`
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
      await refreshStorage();
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
    effortPickerOpen = false;
    workbenchPickerOpen = false;
    if (!activeConversation) {
      setDraftModel(model);
      await tick();
      refreshSuggestions();
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
          '新模型不支持原推理强度，已切换到可用的默认档位。',
          'The new model does not support the previous reasoning effort, so a supported default was selected.'
        );
      }
      await tick();
      refreshSuggestions();
    } catch (error) {
      workspaceError = errorMessage(error);
    }
  }

  function toggleModelPicker() {
    if (generating || activeConversationReadOnly || !selectableModels.length) return;
    modelPickerOpen = !modelPickerOpen;
    effortPickerOpen = false;
    workbenchPickerOpen = false;
  }

  function toggleEffortPicker() {
    if (generating || activeConversationReadOnly || !activeModel) return;
    effortPickerOpen = !effortPickerOpen;
    modelPickerOpen = false;
    workbenchPickerOpen = false;
  }

  function toggleWorkbenchPicker() {
    if (
      generating ||
      activeConversationReadOnly ||
      !restaurantGuidanceEnabled ||
      workbenchUpdating
    ) return;
    workbenchPickerOpen = !workbenchPickerOpen;
    modelPickerOpen = false;
    effortPickerOpen = false;
  }

  async function selectWorkbench(next: 'general' | 'restaurant') {
    if (
      generating ||
      activeConversationReadOnly ||
      workbenchUpdating ||
      next === workbenchSetting?.effective
    ) {
      workbenchPickerOpen = false;
      return;
    }
    workbenchUpdating = true;
    workspaceError = '';
    try {
      const response = await updateWorkbench(next);
      workbenchSetting = response.workbench;
      restaurantGuidanceEnabled = response.guidanceEnabled;
      workbenchPickerOpen = false;
      await tick();
      refreshSuggestions();
    } catch (error) {
      workspaceError = errorMessage(error);
    } finally {
      workbenchUpdating = false;
    }
  }

  async function selectEffort(reasoningEffort: string) {
    if (generating || !supportsEffort(activeModel, reasoningEffort)) return;
    effortPickerOpen = false;
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
    if (generating || activeConversationReadOnly) return;
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

  function replaceConversation(updated: Conversation) {
    conversations = conversations
      .map((conversation) => (conversation.id === updated.id ? updated : conversation))
      .sort((left, right) => {
        if (Boolean(left.pinnedAt) !== Boolean(right.pinnedAt)) {
          return left.pinnedAt ? -1 : 1;
        }
        if (left.pinnedAt && right.pinnedAt && left.pinnedAt !== right.pinnedAt) {
          return right.pinnedAt - left.pinnedAt;
        }
        return right.updatedAt - left.updatedAt;
      });
  }

  async function addFiles(files: File[]) {
    if (!files.length) return;
    if (activeConversationReadOnly) return;
    if (generating || uploading) {
      workspaceError = t(
        '请等待当前操作完成后再添加图片。',
        'Wait for the current operation to finish before adding images.'
      );
      return;
    }
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
    if (activeModel?.capabilitiesComplete && !activeModel.inputModalities?.includes('image')) {
      workspaceError = localizedAPIError(
        'model_image_input_unsupported',
        t('当前模型不支持图片输入。', 'The current model does not support image input.')
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
      await refreshStorage();
    } catch (error) {
      workspaceError = errorMessage(error);
    } finally {
      uploading = false;
    }
  }

  async function chooseFiles(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const files = Array.from(input.files || []);
    input.value = '';
    await addFiles(files);
  }

  function composerPaste(event: ClipboardEvent) {
    const files = Array.from(event.clipboardData?.items || [])
      .filter((item) => item.kind === 'file')
      .map((item) => item.getAsFile())
      .filter((file): file is File => Boolean(file));
    if (!files.length) return;
    event.preventDefault();
    void addFiles(files);
  }

  function isFileDrag(event: DragEvent): boolean {
    return Array.from(event.dataTransfer?.types || []).includes('Files');
  }

  function chatDragEnter(event: DragEvent) {
    if (!isFileDrag(event) || activeConversationReadOnly) return;
    event.preventDefault();
    dragDepth += 1;
  }

  function chatDragOver(event: DragEvent) {
    if (!isFileDrag(event) || activeConversationReadOnly) return;
    event.preventDefault();
  }

  function chatDragLeave(event: DragEvent) {
    if (!isFileDrag(event)) return;
    dragDepth = Math.max(0, dragDepth - 1);
  }

  function chatDrop(event: DragEvent) {
    if (!isFileDrag(event)) return;
    event.preventDefault();
    dragDepth = 0;
    if (activeConversationReadOnly) return;
    void addFiles(Array.from(event.dataTransfer?.files || []));
  }

  async function removeUpload(attachment: Attachment) {
    uploads = uploads.filter((item) => item.id !== attachment.id);
    try {
      await deleteAttachment(attachment.id);
      await refreshStorage();
    } catch {
      // It may already be bound if the response started; the preview can still disappear.
    }
  }

  function resizeComposer() {
    if (!textareaElement) return;
    textareaElement.style.height = '0px';
    textareaElement.style.height = `${Math.min(textareaElement.scrollHeight, 190)}px`;
  }

  function draftKeyFor(conversationId: string): string {
    return `personal-chat-draft:${user?.id || 'anonymous'}:${conversationId || 'new'}`;
  }

  function persistDraft() {
    window.clearTimeout(draftSaveTimer);
    if (!draftStorageKey) return;
    if (text.trim()) {
      localStorage.setItem(draftStorageKey, text);
    } else {
      localStorage.removeItem(draftStorageKey);
    }
  }

  // localStorage writes are synchronous main-thread I/O; batching keystrokes
  // keeps large pasted drafts from adding typing latency. persistDraft is the
  // flush and is still called directly at every state transition.
  function scheduleDraftSave() {
    window.clearTimeout(draftSaveTimer);
    draftSaveTimer = window.setTimeout(persistDraft, 300);
  }

  function restoreDraft(conversationId: string) {
    draftStorageKey = draftKeyFor(conversationId);
    text = localStorage.getItem(draftStorageKey) || '';
    void tick().then(resizeComposer);
  }

  function clearDraft(conversationId: string) {
    localStorage.removeItem(draftKeyFor(conversationId));
  }

  function composerInput() {
    resizeComposer();
    scheduleDraftSave();
  }

  function composerKeydown(event: KeyboardEvent) {
    if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
      event.preventDefault();
      void send();
    }
  }

  function beginGenerationClock(
    stage: GenerationStage = 'sending',
    startedAt = Date.now()
  ) {
    stopGenerationClock();
    generationNow = Date.now();
    generationStartedAt = Math.min(
      generationNow,
      Number.isFinite(startedAt) && startedAt > 0 ? startedAt : generationNow
    );
    generationStage = stage;
    generationTimer = window.setInterval(() => {
      generationNow = Date.now();
    }, 1000);
  }

  function stopGenerationClock() {
    if (generationTimer !== undefined) {
      window.clearInterval(generationTimer);
      generationTimer = undefined;
    }
    generationStartedAt = 0;
    generationStage = '';
  }

  function setGenerationStage(stage: string) {
    const knownStages: GenerationStage[] = [
      'sending',
      'queued',
      'preparing_context',
      'waiting_for_model',
      'reasoning',
      'searching',
      'generating_image',
      'answering',
      'background'
    ];
    if (knownStages.includes(stage as GenerationStage)) {
      generationStage = stage as GenerationStage;
    }
  }

  function generationStageLabel(stage: GenerationStage | ''): string {
    const labels: Record<GenerationStage, [string, string]> = {
      sending: ['正在发送请求', 'Sending request'],
      queued: ['正在等待可用通道', 'Waiting for an available slot'],
      preparing_context: ['正在整理对话上下文', 'Preparing conversation context'],
      waiting_for_model: ['模型已收到请求，正在开始处理', 'The model received the request and is starting'],
      reasoning: ['正在推理并整理思路', 'Reasoning and organizing the approach'],
      searching: ['正在搜索并核对网页', 'Searching and checking web pages'],
      generating_image: ['正在生成图片', 'Generating the image'],
      answering: ['正在组织并输出回答', 'Composing and streaming the answer'],
      background: [
        '回答正在服务器后台继续生成',
        'The response is continuing on the server'
      ]
    };
    return stage ? t(...labels[stage]) : t('正在处理', 'Working');
  }

  // Runs on every `messages` reassignment (dozens per second while
  // streaming), so it must not allocate.
  function findLastUserMessageId(items: Message[]): string {
    for (let index = items.length - 1; index >= 0; index -= 1) {
      if (items[index].role === 'user') return items[index].id;
    }
    return '';
  }

  function isRunningResponse(message: Message | undefined): boolean {
    return message?.status === 'streaming' || message?.status === 'pending';
  }

  function latestRunningResponse(items: Message[]): Message | undefined {
    return [...items]
      .reverse()
      .find((message) => message.role === 'assistant' && isRunningResponse(message));
  }

  async function watchPersistedResponse(
    conversationId: string,
    assistantId: string,
    watchVersion: number,
    initial?: Message
  ): Promise<Message | undefined> {
    let restored = initial;
    let firstPoll = true;
    while (watchVersion === responseWatchVersion) {
      if (restored && !isRunningResponse(restored)) return restored;
      if (!firstPoll) {
        await new Promise((resolve) => window.setTimeout(resolve, 1000));
        if (watchVersion !== responseWatchVersion) break;
      }
      firstPoll = false;
      try {
        restored = await getResponse(assistantId);
        if (watchVersion !== responseWatchVersion) break;
        if (activeConversationId === conversationId) {
          replaceMessage(restored);
          queueScroll();
        }
      } catch (error) {
        if (error instanceof APIError && error.status === 401) {
          clearWorkspaceAndShowLogin();
          return undefined;
        }
        // A temporary viewer/network failure does not control the server-side job.
      }
    }
    return restored;
  }

  async function recoverRunningResponse(
    conversationId: string
  ): Promise<Message | undefined> {
    try {
      const latest = await getMessages(conversationId);
      if (activeConversationId === conversationId) messages = latest;
      return latestRunningResponse(latest);
    } catch {
      return undefined;
    }
  }

  function resumePersistedResponse(conversationId: string) {
    const conversation = conversations.find((item) => item.id === conversationId);
    if (!ownsConversation(conversation)) return;
    const running = latestRunningResponse(messages);
    if (!running) return;

    const watchVersion = ++responseWatchVersion;
    generating = true;
    abortController = null;
    activeAssistantId = running.id;
    activeResponseCancelId = running.id;
    contextStatus = '';
    beginGenerationClock('background', running.createdAt);

    void (async () => {
      const restored = await watchPersistedResponse(
        conversationId, running.id, watchVersion, running
      );
      if (watchVersion !== responseWatchVersion) return;
      if (restored && restored.status !== 'completed' && restored.errorCode) {
        workspaceError = localizedAPIError(
          restored.errorCode,
          t('回答生成未完成。', 'The response did not complete.')
        );
      }
      generating = false;
      stopGenerationClock();
      activeAssistantId = '';
      activeResponseCancelId = '';
      contextStatus = '';
      await refreshCheckpoints(conversationId);
      await refreshStorage();
      queueScroll();
    })();
  }

  // Shared SSE consumer for send/regenerate/edit: one dispatcher, one
  // recovery path, one teardown. Flow-specific behavior is limited to how
  // response.started mutates the transcript and what happens when the
  // request fails before any server-side response exists.
  async function runAssistantStream(options: {
    conversationId: string;
    watchVersion: number;
    start: (
      onEvent: (item: StreamEvent) => void,
      signal: AbortSignal
    ) => Promise<void>;
    onStarted: (data: StreamEvent['data']) => Message;
    onRequestFailed?: () => void;
  }): Promise<void> {
    const { conversationId, watchVersion } = options;
    abortController = new AbortController();
    let assistantId = '';
    try {
      await options.start((item) => {
        if (item.event === 'response.queued') {
          setGenerationStage('queued');
          contextStatus = t(
            `请求排队中（第 ${Number(item.data.position || 1)} 位）`,
            `Request queued (position ${Number(item.data.position || 1)})`
          );
        } else if (item.event === 'response.started') {
          setGenerationStage('preparing_context');
          contextStatus = '';
          const assistantMessage = options.onStarted(item.data);
          assistantId = assistantMessage.id;
          activeAssistantId = assistantMessage.id;
          activeResponseCancelId = assistantMessage.id;
        } else if (item.event === 'response.stage') {
          setGenerationStage(String(item.data.stage || ''));
        } else if (item.event === 'response.reasoning.delta') {
          setGenerationStage('reasoning');
          updateReasoningPart(assistantId, item.data, false);
        } else if (item.event === 'response.reasoning.done') {
          updateReasoningPart(assistantId, item.data, true);
          setGenerationStage('waiting_for_model');
        } else if (item.event === 'response.text.delta') {
          setGenerationStage('answering');
          finishLiveReasoning(assistantId);
          appendTextPart(assistantId, String(item.data.delta || ''));
        } else if (item.event === 'response.tool') {
          finishLiveReasoning(assistantId);
          if (item.data.status === 'in_progress') {
            setGenerationStage(
              item.data.type === 'image_generation' ? 'generating_image' : 'searching'
            );
          } else {
            setGenerationStage('waiting_for_model');
          }
          updateToolPart(assistantId, item.data);
        } else if (item.event === 'response.image' && item.data.attachmentId) {
          appendImagePart(assistantId, String(item.data.attachmentId));
        } else if (item.event === 'response.context') {
          setGenerationStage(
            item.data.status === 'completed' ? 'waiting_for_model' : 'preparing_context'
          );
          contextStatus = contextLabel(String(item.data.status || ''));
          if (item.data.status === 'completed') void refreshCheckpoints(conversationId);
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
      }, abortController.signal);
    } catch (error) {
      const value = errorMessage(error);
      if (!assistantId) {
        const recovered = await recoverRunningResponse(conversationId);
        if (recovered) {
          assistantId = recovered.id;
          activeAssistantId = recovered.id;
          activeResponseCancelId = recovered.id;
        }
      }
      if (assistantId) {
        setGenerationStage('background');
        contextStatus = '';
        workspaceError = '';
        const restored = await watchPersistedResponse(
          conversationId, assistantId, watchVersion
        );
        if (restored?.status === 'completed') {
          workspaceError = '';
        } else if (
          watchVersion === responseWatchVersion &&
          restored &&
          !isRunningResponse(restored)
        ) {
          workspaceError = localizedAPIError(
            restored.errorCode || '',
            value || t('回答生成未完成。', 'The response did not complete.')
          );
        }
      } else {
        if (value) workspaceError = value;
        options.onRequestFailed?.();
      }
    } finally {
      if (watchVersion === responseWatchVersion) {
        generating = false;
        stopGenerationClock();
        abortController = null;
        activeAssistantId = '';
        activeResponseCancelId = '';
        contextStatus = '';
        await refreshCheckpoints(conversationId);
        try {
          conversations = await getConversations(showArchived);
        } catch {
          // The current conversation remains usable if refreshing the sidebar fails.
        }
        await refreshStorage();
        queueScroll();
      }
    }
  }

  async function send() {
    const outgoingText = text.trim();
    const outgoingUploads = [...uploads];
    const outgoingGenerateImage = generateImage;
    if (
      generating ||
      uploading ||
      activeConversationReadOnly ||
      (!outgoingText && outgoingUploads.length === 0)
    ) return;

    speechController.stop();
    let conversation = activeConversation;
    if (!conversation) conversation = await newConversation();
    if (!conversation) return;

    const watchVersion = ++responseWatchVersion;
    const requestId = crypto.randomUUID();
    generating = true;
    activeResponseCancelId = requestId;
    beginGenerationClock('sending');
    followStream = true;
    showJumpToLatest = false;
    workspaceError = '';
    contextStatus = '';
    text = '';
    // The persisted draft is intentionally kept until the server acknowledges
    // the message in response.started, so a failed send cannot lose it.
    uploads = [];
    generateImage = false;
    await tick();
    resizeComposer();

    await runAssistantStream({
      conversationId: conversation.id,
      watchVersion,
      start: (onEvent, signal) =>
        streamResponse(
          conversation.id,
          outgoingText,
          outgoingUploads.map((attachment) => attachment.id),
          requestId,
          outgoingGenerateImage,
          onEvent,
          signal
        ),
      onStarted: (data) => {
        // The server now owns the message; only then may the draft go away.
        persistDraft();
        const userMessage = data.userMessage as Message | undefined;
        const assistantMessage = data.assistantMessage as Message;
        messages = userMessage
          ? [...messages, userMessage, assistantMessage]
          : [...messages, assistantMessage];
        if (conversation.title === 'New chat' && outgoingText) {
          const title = titleFrom(outgoingText);
          void updateConversation(conversation.id, { title }).then(replaceConversation);
        }
        return assistantMessage;
      },
      onRequestFailed: () => {
        // No server-side response exists; restore the composer no matter how
        // the request failed so nothing typed is lost.
        text = outgoingText;
        persistDraft();
        uploads = outgoingUploads;
        generateImage = outgoingGenerateImage;
      }
    });
  }

  async function submitGuidance(submission: GuidanceSubmission) {
    if (
      generating ||
      !activeConversation ||
      activeConversationReadOnly ||
      !restaurantWorkbench
    ) return;
    const conversationId = activeConversation.id;
    const watchVersion = ++responseWatchVersion;
    const requestId = crypto.randomUUID();
    speechController.stop();
    generating = true;
    activeResponseCancelId = requestId;
    beginGenerationClock('sending');
    followStream = true;
    showJumpToLatest = false;
    workspaceError = '';
    contextStatus = '';

    await runAssistantStream({
      conversationId,
      watchVersion,
      start: (onEvent, signal) =>
        streamGuidanceResponse(
          conversationId,
          submission,
          requestId,
          onEvent,
          signal
        ),
      onStarted: (data) => {
        const userMessage = data.userMessage as Message | undefined;
        const assistantMessage = data.assistantMessage as Message;
        messages = userMessage
          ? [...messages, userMessage, assistantMessage]
          : [...messages, assistantMessage];
        return assistantMessage;
      }
    });
  }

  async function regenerate(message: Message, bypassGuidance = false) {
    if (generating || !activeConversation || activeConversationReadOnly) return;
    const conversationId = activeConversation.id;
    const watchVersion = ++responseWatchVersion;
    const requestId = crypto.randomUUID();
    speechController.stop();
    generating = true;
    activeResponseCancelId = requestId;
    beginGenerationClock('sending');
    followStream = true;
    showJumpToLatest = false;
    workspaceError = '';
    contextStatus = '';

    await runAssistantStream({
      conversationId,
      watchVersion,
      start: (onEvent, signal) =>
        regenerateResponse(
          message.id,
          requestId,
          onEvent,
          signal,
          bypassGuidance
        ),
      onStarted: (data) => {
        const assistantMessage = data.assistantMessage as Message;
        messages = [...messages, assistantMessage];
        return assistantMessage;
      }
    });
  }

  function titleFrom(value: string): string {
    const line = value.replace(/\s+/g, ' ').trim();
    return Array.from(line).slice(0, 36).join('') || 'New chat';
  }

  async function beginMessageEdit(message: Message) {
    if (generating || activeConversationReadOnly) return;
    editingMessageId = message.id;
    editingMessageText = messageText(message);
    await tick();
    editTextareaElement?.focus();
  }

  function cancelMessageEdit() {
    editingMessageId = '';
    editingMessageText = '';
  }

  function editKeydown(event: KeyboardEvent, message: Message) {
    if (event.key === 'Escape') {
      event.preventDefault();
      cancelMessageEdit();
      return;
    }
    if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
      event.preventDefault();
      void submitMessageEdit(message);
    }
  }

  async function submitMessageEdit(message: Message) {
    const newText = editingMessageText.trim();
    if (!newText || generating || !activeConversation || activeConversationReadOnly) return;
    if (newText === messageText(message).trim()) {
      cancelMessageEdit();
      return;
    }
    const conversationId = activeConversation.id;
    const watchVersion = ++responseWatchVersion;
    const requestId = crypto.randomUUID();
    cancelMessageEdit();
    speechController.stop();
    generating = true;
    activeResponseCancelId = requestId;
    beginGenerationClock('sending');
    followStream = true;
    showJumpToLatest = false;
    workspaceError = '';
    contextStatus = '';

    await runAssistantStream({
      conversationId,
      watchVersion,
      start: (onEvent, signal) => editResponse(message.id, newText, requestId, onEvent, signal),
      onStarted: (data) => {
        const userMessage = data.userMessage as Message | undefined;
        if (userMessage) replaceMessage(userMessage);
        const assistantMessage = data.assistantMessage as Message;
        messages = [...messages, assistantMessage];
        return assistantMessage;
      },
      onRequestFailed: () => {
        // The edit never reached a server-side response; reopen the editor so
        // the rewritten text is not lost.
        editingMessageId = message.id;
        editingMessageText = newText;
      }
    });
  }

  function appendTextPart(messageId: string, delta: string) {
    if (!messageId || !delta) return;
    messages = messages.map((message) => {
      if (message.id !== messageId) return message;
      const index = message.parts.findIndex((part) => part.type === 'text');
      let parts: MessagePart[];
      if (index < 0) {
        parts = [...message.parts, { type: 'text', text: delta }];
      } else {
        parts = message.parts.map((part, current) =>
          current === index ? { ...part, text: (part.text || '') + delta } : part
        );
      }
      return { ...message, status: 'streaming', parts };
    });
  }

  function reasoningIdentity(data?: Record<string, unknown>): string {
    const itemId = String(data?.providerItemId || data?.itemId || '');
    const outputIndex = Number(data?.outputIndex || 0);
    const summaryIndex = Number(data?.summaryIndex || 0);
    return `${itemId || `output:${outputIndex}`}:${summaryIndex}`;
  }

  function updateReasoningPart(
    messageId: string,
    data: Record<string, unknown>,
    completed: boolean
  ) {
    if (!messageId) return;
    const delta = String(data.delta || '');
    const completeText = String(data.text || '');
    if (!delta && !completeText && !completed) return;
    const identity = reasoningIdentity(data);
    messages = messages.map((message) => {
      if (message.id !== messageId) return message;
      const index = message.parts.findIndex(
        (part) => part.type === 'reasoning' && reasoningIdentity(part.data) === identity
      );
      const now = Date.now();
      const providerData = {
        providerItemId: String(data.itemId || ''),
        outputIndex: Number(data.outputIndex || 0),
        summaryIndex: Number(data.summaryIndex || 0)
      };
      if (index < 0) {
        const next: MessagePart = {
          type: 'reasoning',
          text: completeText || delta,
          data: {
            ...providerData,
            completed,
            clientStartedAt: now,
            ...(Number(data.durationMs || 0) > 0
              ? { durationMs: Number(data.durationMs) }
              : {})
          }
        };
        return { ...message, status: 'streaming', parts: [...message.parts, next] };
      }
      const parts = message.parts.map((part, current) => {
        if (current !== index) return part;
        const startedAt = Number(part.data?.clientStartedAt || now);
        const duration = Number(data.durationMs || 0);
        const isCompleted = completed || part.data?.completed === true;
        return {
          ...part,
          text: completeText || `${part.text || ''}${delta}`,
          data: {
            ...part.data,
            ...providerData,
            completed: isCompleted,
            ...(duration > 0 ? { durationMs: duration } : {}),
            ...(isCompleted && duration <= 0
              ? { clientDurationMs: Math.max(1, now - startedAt) }
              : {})
          }
        };
      });
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
      const previous = index >= 0 ? message.parts[index] : undefined;
      const nextData = { ...data };
      if (data.status === 'in_progress') {
        nextData.clientStartedAt =
          Number(previous?.data?.clientStartedAt || 0) || Date.now();
      }
      const next: MessagePart = { type: 'tool', data: nextData };
      const parts =
        index < 0
          ? [...message.parts, next]
          : message.parts.map((part, current) => (current === index ? next : part));
      return { ...message, parts };
    });
  }

  function finishLiveReasoning(messageId: string) {
    if (!messageId) return;
    const now = Date.now();
    messages = messages.map((message) => {
      if (message.id !== messageId) return message;
      const parts = message.parts.map((part) => {
        if (
          part.type !== 'reasoning' ||
          part.data?.completed === true ||
          part.data?.clientDurationMs
        ) return part;
        const startedAt = Number(part.data?.clientStartedAt || 0);
        if (startedAt <= 0) return part;
        return {
          ...part,
          data: {
            ...part.data,
            completed: true,
            clientDurationMs: Math.max(1, now - startedAt)
          }
        };
      });
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
    messages = messages.some((message) => message.id === updated.id)
      ? messages.map((message) => (message.id === updated.id ? updated : message))
      : [...messages, updated];
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
    if (activeResponseCancelId) {
      try {
        await cancelResponse(activeResponseCancelId);
      } catch {
        workspaceError = t(
          '停止请求未送达服务器；回答可能仍在后台继续。',
          'The stop request did not reach the server; the response may still be continuing.'
        );
        return;
      }
    }
    controller?.abort();
  }

  function handleMessageScroll() {
    if (!scrollElement || scrollStateQueued) return;
    scrollStateQueued = true;
    requestAnimationFrame(() => {
      scrollStateQueued = false;
      if (!scrollElement) return;
      const distanceFromBottom =
        scrollElement.scrollHeight - scrollElement.scrollTop - scrollElement.clientHeight;
      const nearBottom = distanceFromBottom <= 96;
      followStream = nearBottom;
      showJumpToLatest = !nearBottom;
    });
  }

  function pauseStreamFollowing() {
    if (!generating) return;
    followStream = false;
    showJumpToLatest = true;
  }

  function queueScroll(force = false) {
    if (!force && !followStream) {
      showJumpToLatest = true;
      return;
    }
    if (scrollQueued) return;
    scrollQueued = true;
    requestAnimationFrame(() => {
      scrollQueued = false;
      void scrollToBottom(false);
    });
  }

  async function scrollToBottom(smooth: boolean) {
    await tick();
    followStream = true;
    showJumpToLatest = false;
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

  function applyFontSize() {
    document.documentElement.dataset.fontSize = fontSize;
  }

  function selectFontSize(value: FontSize) {
    fontSize = value;
    applyFontSize();
    localStorage.setItem('personal-chat-font-size', value);
  }

  function themeLabel(): string {
    if (theme === 'system') return t('跟随系统', 'Use system theme');
    return theme === 'dark' ? t('深色模式', 'Dark mode') : t('浅色模式', 'Light mode');
  }

  function toggleLocale() {
    setLocale($locale === 'zh-CN' ? 'en' : 'zh-CN');
    profileOpen = false;
  }

  function onboardingStorageKey(): string {
    return user ? `personal-chat-onboarding-v1:${user.id}` : '';
  }

  function openOnboardingIfNeeded() {
    const key = onboardingStorageKey();
    onboardingOpen = Boolean(key && localStorage.getItem(key) !== 'complete');
  }

  function updateAnnouncementStorageKey(): string {
    return user ? `personal-chat-update-ux-v1:${user.id}` : '';
  }

  function openUpdateAnnouncementIfNeeded() {
    const key = updateAnnouncementStorageKey();
    updateAnnouncementOpen = Boolean(
      !onboardingOpen && key && localStorage.getItem(key) !== 'complete'
    );
  }

  function openEntryPrompts() {
    openOnboardingIfNeeded();
    openUpdateAnnouncementIfNeeded();
  }

  function openOnboarding() {
    profileOpen = false;
    sidebarOpen = false;
    workbenchPickerOpen = false;
    modelPickerOpen = false;
    effortPickerOpen = false;
    updateAnnouncementOpen = false;
    onboardingOpen = true;
  }

  async function dismissOnboarding() {
    const key = onboardingStorageKey();
    if (key) localStorage.setItem(key, 'complete');
    onboardingOpen = false;
    openUpdateAnnouncementIfNeeded();
    await tick();
    if (!updateAnnouncementOpen) textareaElement?.focus();
  }

  function openUpdateAnnouncement() {
    profileOpen = false;
    sidebarOpen = false;
    workbenchPickerOpen = false;
    modelPickerOpen = false;
    effortPickerOpen = false;
    onboardingOpen = false;
    updateAnnouncementOpen = true;
  }

  async function dismissUpdateAnnouncement() {
    const key = updateAnnouncementStorageKey();
    if (key) localStorage.setItem(key, 'complete');
    updateAnnouncementOpen = false;
    await tick();
    textareaElement?.focus();
  }

  function effortChoice(effort: string) {
    return reasoningChoices.find((choice) => choice.value === effort);
  }

  function effortLabel(effort: string): string {
    const choice = effortChoice(effort);
    return choice
      ? `${t('推理', 'Reasoning')} · ${t(choice.chinese, choice.english)}`
      : `${t('推理', 'Reasoning')} · ${effort}`;
  }

  function formatBytes(bytes: number): string {
    if (bytes < 1024 * 1024) return `${Math.max(0, bytes / 1024).toFixed(1)} KB`;
    if (bytes < 1024 * 1024 * 1024) {
      return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    }
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`;
  }

  function storagePercent(status: StorageStatus): number {
    if (status.limitBytes <= 0) return 0;
    return Math.min(100, Math.max(0, status.usedBytes / status.limitBytes * 100));
  }

  function refreshSuggestions() {
    const current = new Set(visibleSuggestions.map((suggestion) => suggestion.id));
    const supportsImage = Boolean(activeModel?.imageGenerationMode);
    const source = restaurantWorkbench ? restaurantSuggestionPool : suggestionPool;
    const available = source.filter(
      (suggestion) => suggestion.mode !== 'image' || supportsImage
    );
    const fresh = available.filter((suggestion) => !current.has(suggestion.id));
    const candidates = fresh.length >= 3 ? [...fresh] : [...available];
    for (let index = candidates.length - 1; index > 0; index -= 1) {
      const swap = Math.floor(Math.random() * (index + 1));
      [candidates[index], candidates[swap]] = [candidates[swap], candidates[index]];
    }
    visibleSuggestions = candidates.slice(0, 3);
  }

  async function applySuggestion(suggestion: Suggestion) {
    if (suggestion.mode === 'image') {
      if (!activeModel?.imageGenerationMode) {
        workspaceError = t(
          '当前模型不支持图片生成，请先更换模型。',
          'The current model cannot generate images. Choose another model first.'
        );
        return;
      }
      generateImage = true;
    } else {
      generateImage = false;
    }
    workspaceError = '';
    text = t(suggestion.chinesePrompt, suggestion.englishPrompt);
    persistDraft();
    await tick();
    resizeComposer();
    textareaElement?.focus();
  }

  function queueSearch() {
    window.clearTimeout(searchTimer);
    // Invalidate any in-flight request immediately, otherwise its completion
    // would clear the searching indicator while this newer query is pending.
    searchVersion += 1;
    const query = searchQuery.trim();
    if (!query) {
      searchResults = [];
      searching = false;
      searchError = '';
      return;
    }
    searching = true;
    const version = searchVersion;
    searchTimer = window.setTimeout(() => void runSearch(query, version), 250);
  }

  async function runSearch(query: string, version: number) {
    try {
      const results = await searchConversations(query);
      if (version !== searchVersion) return;
      searchResults = results;
      searchError = '';
    } catch (error) {
      if (version !== searchVersion) return;
      searchError = errorMessage(error);
      searchResults = [];
    } finally {
      if (version === searchVersion) searching = false;
    }
  }

  function clearSearch() {
    window.clearTimeout(searchTimer);
    searchVersion += 1;
    searchQuery = '';
    searchResults = [];
    searching = false;
    searchError = '';
  }

  async function openSearchResult(result: ConversationSearchResult) {
    if (generating) return;
    const id = result.conversation.id;
    clearSearch();
    if (showArchived) {
      showArchived = false;
      try {
        conversations = await getConversations(false);
      } catch {
        // The chat can still open; the sidebar refreshes on the next load.
      }
    }
    if (!conversations.some((conversation) => conversation.id === id)) {
      // Search hits the server live while the sidebar list is a snapshot; the
      // conversation must be present for ownership checks (read-only view of
      // other users' chats) to resolve against it.
      conversations = [result.conversation, ...conversations];
    }
    await openConversation(id);
  }

  async function openUsageDialog() {
    await openDialog('usage');
    usageLoading = true;
    usageError = '';
    try {
      usageRows = await getUsage();
    } catch (error) {
      usageError = errorMessage(error);
    } finally {
      usageLoading = false;
    }
  }

  function usageMonths(rows: UsageRow[]): string[] {
    const months: string[] = [];
    for (const row of rows) {
      if (!months.includes(row.month)) months.push(row.month);
    }
    return months;
  }

  function usageRowsFor(rows: UsageRow[], month: string): UsageRow[] {
    return rows.filter((row) => row.month === month);
  }

  function usageMonthLabel(month: string): string {
    const [year, monthNumber] = month.split('-');
    if (!year || !monthNumber) return month;
    return t(`${year} 年 ${Number(monthNumber)} 月`, `${year}-${monthNumber}`);
  }

  function usageModelLabel(modelId: string): string {
    const known = models.find((item) => item.id === modelId);
    const mode = modelModeInfo(known);
    if (mode) {
      return `${t(mode.chineseName, mode.englishName)} · ${mode.technicalName}`;
    }
    return known?.name || modelId || t('未知模型', 'Unknown model');
  }

  function usageOwnerLabel(row: UsageRow): string {
    return row.ownerDisplayName || row.ownerUsername || '';
  }

  function modelModeInfo(model: Model | null | undefined): ModelModeInfo | null {
    if (!model) return null;
    const id = model.id.toLocaleLowerCase();
    if (!id.startsWith('gpt')) return null;
    if (/(^|[-_.])luna($|[-_.])/.test(id)) {
      return {
        key: 'fast',
        chineseName: '快速',
        englishName: 'Fast',
        technicalName: 'Luna',
        chineseDescription: '响应最快的模型，适合日常问答、改写和快速检索。',
        englishDescription: 'The fastest model, suited to everyday questions, rewriting, and quick searches.'
      };
    }
    if (/(^|[-_.])terra($|[-_.])/.test(id)) {
      return {
        key: 'balanced',
        chineseName: '均衡',
        englishName: 'Balanced',
        technicalName: 'Terra',
        chineseDescription: '智能与速度均衡的模型，适合大多数分析、写作和多步骤任务。',
        englishDescription: 'A model balancing intelligence and speed, suited to analysis, writing, and multi-step tasks.'
      };
    }
    if (/(^|[-_.])sol($|[-_.])/.test(id)) {
      return {
        key: 'expert',
        chineseName: '专家',
        englishName: 'Expert',
        technicalName: 'Sol',
        chineseDescription: '最高智能的模型，适合复杂推理、编程和高要求任务。',
        englishDescription: 'The most intelligent model, suited to complex reasoning, coding, and demanding work.'
      };
    }
    return null;
  }

  function visibleModeModels(source: Model[]): Model[] {
    const selected = new Map<ModelMode, Model>();
    for (const model of source) {
      const mode = modelModeInfo(model);
      if (model.selectable && mode && !selected.has(mode.key)) {
        selected.set(mode.key, model);
      }
    }
    return (['fast', 'balanced', 'expert'] as const)
      .map((mode) => selected.get(mode))
      .filter((model): model is Model => Boolean(model));
  }

  function modelDisplayName(model: Model | null | undefined): string {
    const mode = modelModeInfo(model);
    return mode ? t(mode.chineseName, mode.englishName) : model?.name || '';
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
      <form
        autocomplete={rememberPassword ? 'on' : 'off'}
        on:submit|preventDefault={submitLogin}
      >
        <label>
          <span>{t('用户名', 'Username')}</span>
          <input
            name="username"
            bind:value={loginUsername}
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
            bind:value={loginPassword}
            autocomplete={rememberPassword ? 'current-password' : 'off'}
            required
            placeholder={t('输入密码', 'Enter your password')}
          />
        </label>
        <label class="remember-login">
          <input name="rememberPassword" type="checkbox" bind:checked={rememberPassword} />
          <span>
            <strong>{t('记住密码', 'Remember password')}</strong>
            <small>
              {t(
                '交由浏览器密码管理器安全保存，本网站不会把密码写入本地存储。',
                'Saved securely by your browser password manager; this site never writes the password to local storage.'
              )}
            </small>
          </span>
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
    <footer>La4RainGPT · Based on Open WebUI</footer>
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
          <span>La4RainGPT</span>
        </div>
        <button
          class="icon-button mobile-close"
          aria-label={t('关闭', 'Close')}
          on:click={() => (sidebarOpen = false)}
        >
          <Icon name="close" size={20} />
        </button>
      </div>
      <button
        class="new-chat"
        class:pending={creatingConversation}
        aria-busy={creatingConversation}
        on:click={startNewChat}
        disabled={generating || creatingConversation || !selectableModels.length}
      >
        <span class="plus">
          {#if creatingConversation}
            <span class="toolbar-spinner" aria-hidden="true"></span>
          {:else}
            <Icon name="plus" size={18} />
          {/if}
        </span>
        <span>{creatingConversation ? t('正在创建…', 'Creating…') : t('新对话', 'New chat')}</span>
        <kbd>⌘ K</kbd>
      </button>

      <div class="sidebar-search">
        <span class="sidebar-search-icon"><Icon name="search" size={15} /></span>
        <input
          type="search"
          placeholder={t('搜索对话', 'Search chats')}
          aria-label={t('搜索对话', 'Search chats')}
          bind:value={searchQuery}
          on:input={queueSearch}
          on:keydown={(event) => {
            if (event.key === 'Escape') clearSearch();
          }}
        />
        {#if searchQuery}
          <button
            type="button"
            class="sidebar-search-clear"
            aria-label={t('清除搜索', 'Clear search')}
            on:click={clearSearch}
          ><Icon name="close" size={14} /></button>
        {/if}
      </div>

      <div class="history-label">
        {searchQuery.trim()
          ? t('搜索结果', 'Search results')
          : showArchived
            ? t('临时留档 · 7 天', 'Retained · 7 days')
            : t('对话记录', 'Chats')}
      </div>
      {#if searchQuery.trim()}
        <nav class="conversation-list search-results" aria-label={t('搜索结果', 'Search results')}>
          {#if searching}
            <p class="no-history">{t('正在搜索…', 'Searching…')}</p>
          {:else if searchError}
            <p class="no-history">{searchError}</p>
          {:else}
            {#each searchResults as result (result.conversation.id)}
              <button
                type="button"
                class="search-result"
                on:click={() => openSearchResult(result)}
              >
                <span class="conversation-icon">
                  <Icon name={result.matchedIn === 'title' ? 'chat' : 'search'} size={16} />
                </span>
                <span class="conversation-copy">
                  <span class="conversation-title">{result.conversation.title}</span>
                  {#if result.snippet}
                    <small class="search-snippet">{result.snippet}</small>
                  {/if}
                  {#if user?.role === 'admin' && result.conversation.ownerUsername}
                    <small>
                      {result.conversation.ownerDisplayName || result.conversation.ownerUsername}
                      {result.conversation.ownerId === user.id ? t('（我）', ' (me)') : ''}
                    </small>
                  {/if}
                </span>
              </button>
            {/each}
            {#if searchResults.length === 0}
              <p class="no-history">{t('没有匹配的对话', 'No matching chats')}</p>
            {/if}
          {/if}
        </nav>
      {:else}
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
                <span class:pinned={Boolean(conversation.pinnedAt)} class="conversation-icon">
                  <Icon name={conversation.pinnedAt ? 'pin' : 'chat'} size={16} />
                </span>
                <span class="conversation-copy">
                  <span class="conversation-title">{conversation.title}</span>
                  {#if user?.role === 'admin'}
                    <small>
                      {conversation.ownerDisplayName || conversation.ownerUsername}
                      {conversation.ownerId === user.id ? t('（我）', ' (me)') : ''}
                    </small>
                  {:else if showArchived && conversation.retentionReason === 'conversation_limit'}
                    <small>{t('因超过 30 个对话自动留档', 'Retained after reaching 30 chats')}</small>
                  {/if}
                </span>
              </button>
            {/if}
            {#if ownsConversation(conversation)}
              <span class="conversation-actions">
                {#if !showArchived}
                  <button
                    type="button"
                    class:active={Boolean(conversation.pinnedAt)}
                    class="mini-action"
                    aria-label={conversation.pinnedAt ? t('取消置顶', 'Unpin') : t('置顶', 'Pin')}
                    title={conversation.pinnedAt ? t('取消置顶', 'Unpin') : t('置顶（最多 10 个）', 'Pin (up to 10)')}
                    on:click={(event) => togglePinned(event, conversation)}
                  ><Icon name={conversation.pinnedAt ? 'pin-off' : 'pin'} size={15} /></button>
                {/if}
                <button
                  type="button"
                  class="mini-action"
                  aria-label={showArchived ? t('恢复', 'Restore') : t('临时留档', 'Retain')}
                  title={showArchived
                    ? t('恢复到活跃对话', 'Restore to active chats')
                    : t('临时留档 7 天', 'Retain for 7 days')}
                  on:click={(event) => setArchived(event, conversation, !showArchived)}
                ><Icon name={showArchived ? 'restore' : 'archive'} size={15} /></button>
                {#if !showArchived}
                  <button
                    type="button"
                    class="mini-action"
                    aria-label={t('重命名', 'Rename')}
                    title={t('重命名', 'Rename')}
                    on:click={(event) => beginRename(event, conversation)}
                  ><Icon name="edit" size={15} /></button>
                {/if}
                <button
                  type="button"
                  class="mini-action danger"
                  aria-label={t('永久删除', 'Delete permanently')}
                  title={t('永久删除', 'Delete permanently')}
                  on:click={(event) => removeConversation(event, conversation)}
                ><Icon name="trash" size={15} /></button>
              </span>
            {/if}
          </div>
        {/each}
        {#if conversations.length === 0}
          <p class="no-history">
            {showArchived
              ? t('没有临时留档的对话', 'No retained chats')
              : t('开始第一段对话吧', 'Start your first chat')}
          </p>
        {/if}
      </nav>
      {/if}

      {#if storageStatus}
        <section class="storage-card" aria-label={t('我的空间用量', 'My storage usage')}>
          <div class="storage-heading">
            <span>{t('我的空间', 'My storage')}</span>
            <strong>{formatBytes(storageStatus.usedBytes)} / {formatBytes(storageStatus.limitBytes)}</strong>
          </div>
          <div
            class:warning={storagePercent(storageStatus) >= 85}
            class="storage-track"
            role="progressbar"
            aria-valuemin="0"
            aria-valuemax="100"
            aria-valuenow={Math.round(storagePercent(storageStatus))}
          ><i style={`width: ${storagePercent(storageStatus)}%`}></i></div>
          <div class="storage-meta">
            <span>{storageStatus.activeConversations}/{storageStatus.maxActiveConversations} {t('对话', 'chats')}</span>
            <span>{storageStatus.pinnedConversations}/{storageStatus.maxPinnedConversations} {t('置顶', 'pinned')}</span>
          </div>
          {#if storageStatus.retainedBytes > 0}
            <small>
              {t('临时留档', 'Retained')} {formatBytes(storageStatus.retainedBytes)}
              · {t('不计入限额', 'excluded from quota')}
            </small>
          {/if}
        </section>
      {/if}

      <div class="sidebar-footer">
        <button
          class="profile-button"
          aria-haspopup="true"
          aria-expanded={profileOpen}
          aria-controls="profile-menu"
          on:click={() => (profileOpen = !profileOpen)}
        >
          <span class="profile-avatar">{user?.displayName?.slice(0, 1).toUpperCase()}</span>
          <span class="profile-copy">
            <strong>{user?.displayName}</strong>
            <small>
              @{user?.username}{user?.role === 'admin' ? ` · ${t('管理员', 'Admin')}` : ''}
            </small>
          </span>
          <Icon name="more" size={18} />
        </button>
        {#if profileOpen}
          <div class="profile-menu" id="profile-menu">
            <button on:click={toggleTheme}>
              <Icon
                name={theme === 'system' ? 'theme' : resolvedTheme === 'dark' ? 'sun' : 'moon'}
                size={17}
              />
              {themeLabel()}
            </button>
            <button on:click={() => openDialog('appearance')}>
              <Icon name="text-size" size={17} />{t('显示与字体', 'Display & text')}
            </button>
            <button on:click={() => openDialog('speech')}>
              <Icon name="speaker" size={17} />{t('语音与朗读', 'Speech & read aloud')}
            </button>
            <button on:click={toggleArchiveView}>
              <Icon name={showArchived ? 'chat' : 'archive'} size={17} />
              {showArchived ? t('返回对话记录', 'Back to chats') : t('临时留档', 'Retained chats')}
            </button>
            <button on:click={toggleLocale}>
              <Icon name="globe" size={17} />{$locale === 'zh-CN' ? 'English' : '中文'}
            </button>
            <button on:click={openUsageDialog}>
              <Icon name="plan" size={17} />{t('用量统计', 'Usage stats')}
            </button>
            <button on:click={openOnboarding}>
              <Icon name="plan" size={17} />{t('新手指南', 'Getting started')}
            </button>
            <button on:click={openUpdateAnnouncement}>
              <Icon name="speaker" size={17} />{t('更新公告', 'What’s new')}
            </button>
            <button on:click={reloadApplication}><Icon name="refresh" size={17} />{t('刷新应用', 'Reload app')}</button>
            {#if user?.role === 'admin'}
              <button on:click={() => openDialog('service')}><Icon name="sparkles" size={17} />{t('推理摘要设置', 'Reasoning summary settings')}</button>
              <button on:click={() => openDialog('speech-admin')}><Icon name="speaker" size={17} />{t('语音服务设置', 'Speech service settings')}</button>
            {/if}
            <button on:click={() => openDialog('security')}><Icon name="shield" size={17} />{t('账户与安全', 'Account & security')}</button>
            <button on:click={() => openDialog('about')}><Icon name="info" size={17} />{t('关于', 'About')}</button>
            <button class="logout-button" on:click={doLogout}><Icon name="logout" size={17} />{t('退出登录', 'Sign out')}</button>
          </div>
        {/if}
      </div>
    </aside>

    <main
      class="chat-panel"
      on:dragenter={chatDragEnter}
      on:dragover={chatDragOver}
      on:dragleave={chatDragLeave}
      on:drop={chatDrop}
    >
      {#if dragActive && !activeConversationReadOnly && !generating && !uploading}
        <div class="drop-overlay" aria-hidden="true">
          <div class="drop-overlay-card">
            <Icon name="image-plus" size={22} />
            <strong>{t('松开即可上传图片', 'Drop images to upload')}</strong>
            <small>{t('支持 PNG、JPEG、WebP，最多 4 张', 'PNG, JPEG, or WebP · up to 4 images')}</small>
          </div>
        </div>
      {/if}
      <header class="chat-header">
        <button
          class="icon-button menu-button"
          aria-label={t('打开侧边栏', 'Open sidebar')}
          on:click={() => (sidebarOpen = true)}
        >
          <Icon name="menu" size={20} />
        </button>
        <div class="selectors">
          {#if workbenchPickerOpen || modelPickerOpen || effortPickerOpen}
            <button
              class="model-picker-scrim"
              aria-label={t('关闭选择列表', 'Close picker')}
              on:click={() => {
                workbenchPickerOpen = false;
                modelPickerOpen = false;
                effortPickerOpen = false;
              }}
            ></button>
          {/if}
          {#if restaurantGuidanceEnabled && workbenchSetting?.initial === 'restaurant'}
            <div class="workbench-picker">
              <button
                type="button"
                class:restaurant={restaurantWorkbench}
                class="workbench-picker-trigger"
                aria-haspopup="listbox"
                aria-expanded={workbenchPickerOpen}
                on:click={toggleWorkbenchPicker}
                disabled={generating || activeConversationReadOnly || workbenchUpdating}
              >
                <Icon name={restaurantWorkbench ? 'plan' : 'chat'} size={15} />
                <span>
                  {restaurantWorkbench
                    ? t('餐饮任务', 'Restaurant')
                    : t('通用聊天', 'General')}
                </span>
                <Icon name="chevron-down" size={14} />
              </button>
              {#if workbenchPickerOpen}
                <div class="workbench-picker-panel">
                  <div class="picker-heading">
                    <strong>{t('选择工作台', 'Choose a workbench')}</strong>
                    <small>
                      {t(
                        '餐饮任务会在问题模糊时先用按钮帮你补充需求',
                        'Restaurant mode uses buttons to refine vague requests'
                      )}
                    </small>
                  </div>
                  <div class="workbench-options" role="listbox" aria-label={t('工作台', 'Workbench')}>
                    <button
                      type="button"
                      role="option"
                      aria-selected={workbenchSetting?.effective === 'general'}
                      class:selected={workbenchSetting?.effective === 'general'}
                      on:click={() => selectWorkbench('general')}
                    >
                      <span class="workbench-option-icon"><Icon name="chat" size={16} /></span>
                      <span>
                        <strong>{t('通用聊天', 'General chat')}</strong>
                        <small>{t('直接按普通对话方式回答', 'Use the standard chat flow')}</small>
                      </span>
                      {#if workbenchSetting?.effective === 'general'}
                        <span class="model-check"><Icon name="check" size={17} /></span>
                      {/if}
                    </button>
                    <button
                      type="button"
                      role="option"
                      aria-selected={workbenchSetting?.effective === 'restaurant'}
                      class:selected={workbenchSetting?.effective === 'restaurant'}
                      on:click={() => selectWorkbench('restaurant')}
                    >
                      <span class="workbench-option-icon"><Icon name="plan" size={16} /></span>
                      <span>
                        <strong>{t('餐饮任务', 'Restaurant tasks')}</strong>
                        <small>
                          {t(
                            '模糊问题先澄清，再确认任务简报',
                            'Refine vague requests, then confirm a task brief'
                          )}
                        </small>
                      </span>
                      {#if workbenchSetting?.effective === 'restaurant'}
                        <span class="model-check"><Icon name="check" size={17} /></span>
                      {/if}
                    </button>
                  </div>
                </div>
              {/if}
            </div>
          {/if}
          <div class="model-picker">
            <button
              type="button"
              class="model-picker-trigger"
              aria-haspopup="listbox"
              aria-expanded={modelPickerOpen}
              on:click={toggleModelPicker}
              disabled={generating || activeConversationReadOnly || !selectableModels.length}
            >
              <span>{modelDisplayName(activeModel) || activeConversation?.model || draftModel || t('选择模式', 'Select mode')}</span>
              <Icon name="chevron-down" size={15} />
            </button>
            {#if modelPickerOpen}
              <div class="model-picker-panel">
                <div class="model-tier-guide">
                  <strong>{t('选择工作模式', 'Choose a mode')}</strong>
                  <span>{t('快速 · 均衡 · 专家', 'Fast · Balanced · Expert')}</span>
                  <small>
                    {t(
                      '三种模式分别由 GPT Luna、Terra 和 Sol 提供，可随时为当前会话切换。',
                      'The three modes use GPT Luna, Terra, and Sol respectively and can be changed per chat.'
                    )}
                  </small>
                </div>
                <div class="model-options" role="listbox" aria-label={t('聊天模型', 'Chat models')}>
                  {#if activeConversation &&
                    !selectableModels.some((model) => model.id === activeConversation?.model)}
                    <div class="model-unavailable">
                      <strong>{activeConversation.model}</strong>
                      <span>{t('这是旧会话使用的隐藏模型，请切换到以下模式继续。', 'This older chat uses a hidden model. Choose a mode below to continue.')}</span>
                    </div>
                  {/if}
                  {#each selectableModels as model}
                    {@const mode = modelModeInfo(model)}
                    <button
                      type="button"
                      role="option"
                      aria-selected={model.id === (activeConversation?.model || draftModel)}
                      class:selected={model.id === (activeConversation?.model || draftModel)}
                      on:click={() => selectModel(model.id)}
                    >
                      <span class="model-option-copy">
                        <span class="model-option-heading">
                          <strong>{modelDisplayName(model)}</strong>
                          {#if mode}<em>GPT · {mode.technicalName}</em>{/if}
                        </span>
                        {#if mode}<small>{t(mode.chineseDescription, mode.englishDescription)}</small>{/if}
                      </span>
                      {#if model.id === (activeConversation?.model || draftModel)}
                        <span class="model-check"><Icon name="check" size={17} /></span>
                      {/if}
                    </button>
                  {/each}
                  {#if selectableModels.length === 0}
                    <p>{t('CPA 暂未提供可用的 GPT 模式', 'CPA has not provided an available GPT mode')}</p>
                  {/if}
                </div>
              </div>
            {/if}
          </div>
          {#if activeModel}
            <div class="effort-picker">
              <button
                type="button"
                class="effort-picker-trigger"
                aria-label={t('推理强度', 'Reasoning effort')}
                aria-haspopup="listbox"
                aria-expanded={effortPickerOpen}
                title={t(
                  '更高推理强度通常响应更慢并消耗更多额度；这里展示的是安全的推理摘要，不是原始思维链。',
                  'Higher reasoning effort is usually slower and uses more quota. The UI shows safe reasoning summaries, not raw chain of thought.'
                )}
                on:click={toggleEffortPicker}
                disabled={generating || activeConversationReadOnly}
              >
                <span class="effort-icon"><Icon name="sparkles" size={13} /></span>
                <span>{effortLabel(selectedReasoningEffort)}</span>
                <Icon name="chevron-down" size={14} />
              </button>
              {#if effortPickerOpen}
                <div class="effort-picker-panel">
                  <div class="picker-heading">
                    <strong>{t('推理强度', 'Reasoning effort')}</strong>
                    <small>{t('选择本对话的思考深度', 'Choose how deeply this chat should reason')}</small>
                  </div>
                  <div class="effort-options" role="listbox" aria-label={t('推理强度', 'Reasoning effort')}>
                    {#each reasoningChoices as choice}
                      <button
                        type="button"
                        role="option"
                        class:selected={choice.value === selectedReasoningEffort}
                        aria-selected={choice.value === selectedReasoningEffort}
                        disabled={!supportsEffort(activeModel, choice.value)}
                        on:click={() => selectEffort(choice.value)}
                      >
                        <span class="effort-option-icon"><Icon name="sparkles" size={14} /></span>
                        <span class="effort-option-copy">
                          <strong>{t(choice.chinese, choice.english)}</strong>
                          <small>{t(choice.chineseDescription, choice.englishDescription)}</small>
                          <code>{choice.value}</code>
                        </span>
                        {#if choice.value === selectedReasoningEffort}
                          <span class="model-check"><Icon name="check" size={17} /></span>
                        {/if}
                      </button>
                    {/each}
                  </div>
                  <p>
                    {t(
                      '档位依次映射到 CPA 的 medium / high / max。',
                      'Levels map to CPA medium / high / max respectively.'
                    )}
                  </p>
                </div>
              {/if}
            </div>
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

      {#if viewingOtherUser && activeConversation}
        <div class="readonly-banner" role="status">
          <Icon name="eye" size={15} />
          <span>
            {t('管理员只读查看', 'Administrator read-only view')}
            · {activeConversation.ownerDisplayName || activeConversation.ownerUsername}
          </span>
        </div>
      {/if}

      <div
        class:speech-active={Boolean(
          $speechPlayerState.messageId || $speechDeviceAuthorization.needsGesture
        )}
        class="messages-scroll"
        bind:this={scrollElement}
        on:scroll={handleMessageScroll}
        on:wheel={(event) => {
          if (event.deltaY < 0) pauseStreamFollowing();
        }}
        on:touchstart={pauseStreamFollowing}
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
            <h2>
              {restaurantWorkbench
                ? t('今天想解决哪件餐饮任务？', 'Which restaurant task should we tackle?')
                : t('今天想聊点什么？', 'What would you like to talk about?')}
            </h2>
            <p>
              {restaurantWorkbench
                ? t(
                    '先说个大概就行；需要时我会给出可点击选项，帮你把需求一步步说清楚。',
                    'Start with a rough idea. When needed, I’ll offer tappable choices to refine it.'
                  )
                : t(
                    '我可以帮你写作、分析、搜索网页，也能理解和生成图片。',
                    'I can help you write, analyze, search the web, and understand or generate images.'
                  )}
            </p>
            <div class="suggestion-heading">
              <span>{t('试试这些', 'Try one of these')}</span>
              <button type="button" on:click={refreshSuggestions}>
                <Icon name="refresh" size={15} />
                {t('换一批', 'Refresh')}
              </button>
            </div>
            <div class="suggestion-grid">
              {#each visibleSuggestions as suggestion (suggestion.id)}
                <button type="button" on:click={() => applySuggestion(suggestion)}>
                  <span><Icon name={suggestion.icon} size={18} /></span>
                  <strong>{t(suggestion.chineseTitle, suggestion.englishTitle)}</strong>
                  <small>{t(suggestion.chinesePrompt, suggestion.englishPrompt)}</small>
                </button>
              {/each}
            </div>
          </section>
        {:else}
          <div class="message-column">
            {#each messages as message (message.id)}
              {#if editingMessageId === message.id}
                <div class="message-edit">
                  <div class="message-edit-heading">
                    <Icon name="edit" size={15} />
                    <strong>{t('编辑消息', 'Edit message')}</strong>
                    <small>
                      {t(
                        '重新发送后会生成新的回答，原回答仍会保留',
                        'Resending creates a new answer; earlier answers stay in the chat'
                      )}
                    </small>
                  </div>
                  <textarea
                    bind:this={editTextareaElement}
                    bind:value={editingMessageText}
                    rows="3"
                    aria-label={t('编辑消息内容', 'Edited message text')}
                    on:keydown={(event) => editKeydown(event, message)}
                  ></textarea>
                  <div class="message-edit-actions">
                    <button type="button" class="message-edit-cancel" on:click={cancelMessageEdit}>
                      {t('取消', 'Cancel')}
                    </button>
                    <button
                      type="button"
                      class="message-edit-send"
                      disabled={!editingMessageText.trim()}
                      on:click={() => submitMessageEdit(message)}
                    >
                      <Icon name="send" size={15} />
                      {t('重新发送', 'Resend')}
                    </button>
                  </div>
                </div>
              {:else}
              <MessageView
                {message}
                locale={$locale}
                streamingStage={message.id === activeAssistantId
                  ? generationStageLabel(generationStage)
                  : ''}
                streamNow={generationNow}
                elapsedSeconds={message.id === activeAssistantId
                  ? generationElapsedSeconds
                  : 0}
                canRegenerate={message.role === 'assistant' &&
                  message.id === messages.at(-1)?.id &&
                  !generating &&
                  !activeConversationReadOnly}
                canEdit={message.role === 'user' &&
                  message.id === lastUserMessageId &&
                  message.parts.some((part) => part.type === 'text' && part.text) &&
                  !generating &&
                  !activeConversationReadOnly}
                userId={user?.id || ''}
                guidanceCurrent={restaurantWorkbench &&
                  !activeConversationReadOnly &&
                  message.id === messages.at(-1)?.id &&
                  (message.status === 'completed' || message.status === 'error')}
                guidanceDisabled={generating}
                guidanceDraftEnabled={!viewingOtherUser && !showArchived}
                on:regenerate={(event) => regenerate(event.detail.message)}
                on:edit={(event) => beginMessageEdit(event.detail.message)}
                on:guidanceSubmit={(event) => submitGuidance(event.detail.submission)}
                on:guidanceRetry={(event) =>
                  regenerate(event.detail.message, event.detail.bypass)}
              />
              {/if}
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

      <div
        class:speech-active={Boolean(
          $speechPlayerState.messageId || $speechDeviceAuthorization.needsGesture
        )}
        class="composer-zone"
      >
        {#if showJumpToLatest}
          <button
            type="button"
            class="jump-to-latest"
            on:click={() => scrollToBottom(true)}
          >
            <Icon name="chevron-down" size={15} />
            {t('查看最新回答', 'Jump to latest')}
          </button>
        {/if}
        {#if contextStatus || (generating && !activeAssistantId)}
          <div class="context-status" role="status">
            <span><Icon name="sparkles" size={14} /></span>
            <strong>{contextStatus || generationStageLabel(generationStage)}</strong>
            {#if generating}<time>{generationElapsedSeconds} s</time>{/if}
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
        {#if $speechDeviceAuthorization.needsGesture && $speechPreference?.mode === 'auto'}
          <div class="speech-authorization-prompt" role="status">
            <span><Icon name="speaker" size={16} /></span>
            <strong>{t('自动朗读需要当前设备允许播放声音', 'Auto-read needs audio permission on this device')}</strong>
            <button type="button" on:click={authorizeSpeechPlayback}>
              {t('允许播放', 'Enable audio')}
            </button>
          </div>
        {/if}
        <SpeechPlayer locale={$locale} />
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
            on:input={composerInput}
            on:keydown={composerKeydown}
            on:paste={composerPaste}
            placeholder={viewingOtherUser
              ? t('管理员查看其他用户会话时为只读', 'Other users’ chats are read-only for administrators')
              : showArchived
              ? t('临时留档对话为只读', 'Retained chats are read-only')
              : generateImage
                ? t('描述你想生成的图片', 'Describe the image you want to create')
                : t('给 AI 发送消息', 'Message AI')}
            rows="1"
            aria-label={generateImage
              ? t('图片生成描述', 'Image generation prompt')
              : t('聊天消息', 'Chat message')}
            disabled={generating || activeConversationReadOnly}
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
                  activeConversationReadOnly ||
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
                  activeConversationReadOnly ||
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
                disabled={activeConversationReadOnly ||
                  uploading ||
                  (!text.trim() && uploads.length === 0)}
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

  {#if onboardingOpen}
    <Onboarding locale={$locale} on:dismiss={dismissOnboarding} />
  {/if}

  {#if updateAnnouncementOpen}
    <UpdateAnnouncement locale={$locale} on:dismiss={dismissUpdateAnnouncement} />
  {/if}

  {#if dialog}
    <div class="modal-layer">
      <button
        class="modal-backdrop"
        aria-label={t('关闭对话框', 'Close dialog')}
        on:click={() => (dialog = '')}
      ></button>
      <div
        class:speech-dialog={dialog === 'speech' || dialog === 'speech-admin'}
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
              '修改密码会注销这个账户在所有设备上的会话。新密码至少需要 6 个字符。',
              'Changing your password signs this account out on every device. The new password must be at least 6 characters.'
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
                minlength="6"
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
                minlength="6"
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
        {:else if dialog === 'appearance'}
          <div class="dialog-icon"><Icon name="text-size" size={23} /></div>
          <h2 id="dialog-title">{t('显示与字体', 'Display & text')}</h2>
          <p class="dialog-lead">
            {t(
              '选择适合当前设备的字号。页面会整体重排，消息正文与输入框始终保留清晰的阅读下限。',
              'Choose a size for this device. The whole layout reflows while message text and inputs retain a readable minimum.'
            )}
          </p>
          <div class="font-preview" aria-label={t('字体效果预览', 'Text size preview')}>
            <span>{t('效果预览', 'Preview')}</span>
            <p>{t('清晰阅读，也让界面保持从容。', 'Comfortable reading with a calm layout.')}</p>
            <small>{t('设置会立即应用到侧栏、对话和弹窗。', 'The setting applies immediately to chats, navigation, and dialogs.')}</small>
          </div>
          <div class="font-size-options" role="radiogroup" aria-label={t('字体大小', 'Text size')}>
            {#each fontSizeChoices as choice}
              <button
                type="button"
                role="radio"
                class:selected={choice.value === fontSize}
                aria-checked={choice.value === fontSize}
                on:click={() => selectFontSize(choice.value)}
              >
                <span>
                  <strong>{t(choice.chinese, choice.english)}</strong>
                  <small>{t(choice.chineseDescription, choice.englishDescription)}</small>
                </span>
                {#if choice.value === fontSize}
                  <Icon name="check" size={17} />
                {/if}
              </button>
            {/each}
          </div>
          <p class="appearance-note">
            {t(
              '此设置只保存在当前浏览器，不会影响其他设备。',
              'This setting is stored in this browser and does not affect other devices.'
            )}
          </p>
        {:else if dialog === 'usage'}
          <div class="dialog-icon"><Icon name="plan" size={23} /></div>
          <h2 id="dialog-title">{t('用量统计', 'Usage stats')}</h2>
          <p class="dialog-lead">
            {t(
              '按月统计已完成回答的次数与 token 消耗。所有用户共享同一个上游额度。',
              'Monthly totals of completed responses and token usage. All users share one upstream quota.'
            )}
          </p>
          {#if usageLoading}
            <p class="usage-empty">{t('正在加载…', 'Loading…')}</p>
          {:else if usageError}
            <div class="account-error" role="alert">{usageError}</div>
          {:else if usageRows.length === 0}
            <p class="usage-empty">{t('还没有任何用量记录。', 'No usage recorded yet.')}</p>
          {:else}
            <div class="usage-months">
              {#each usageMonths(usageRows) as month (month)}
                <section class="usage-month">
                  <h3>{usageMonthLabel(month)}</h3>
                  <div class="usage-table" role="table">
                    <div class="usage-row usage-head" role="row">
                      <span role="columnheader">{t('模型', 'Model')}</span>
                      <span role="columnheader">{t('回答', 'Responses')}</span>
                      <span role="columnheader">{t('输入 tokens', 'Input tokens')}</span>
                      <span role="columnheader">{t('输出 tokens', 'Output tokens')}</span>
                    </div>
                    {#each usageRowsFor(usageRows, month) as row (row.model + (row.ownerId || ''))}
                      <div class="usage-row" role="row">
                        <span role="cell">
                          {usageModelLabel(row.model)}
                          {#if user?.role === 'admin' && usageOwnerLabel(row)}
                            <small>{usageOwnerLabel(row)}</small>
                          {/if}
                        </span>
                        <span role="cell">{row.responses.toLocaleString($locale)}</span>
                        <span role="cell">{row.inputTokens.toLocaleString($locale)}</span>
                        <span role="cell">
                          {row.outputTokens.toLocaleString($locale)}
                          {#if row.reasoningTokens}
                            <small>
                              {t('含推理', 'incl. reasoning')} {row.reasoningTokens.toLocaleString($locale)}
                            </small>
                          {/if}
                        </span>
                      </div>
                    {/each}
                  </div>
                </section>
              {/each}
            </div>
            <p class="appearance-note">
              {t(
                '统计按 UTC 月份汇总，仅包含已完成或中断的回答。',
                'Months are aggregated in UTC and include only finished responses.'
              )}
            </p>
          {/if}
        {:else if dialog === 'speech'}
          <SpeechSettings locale={$locale} />
        {:else if dialog === 'service'}
          <ProgressiveSummarySettings locale={$locale} />
        {:else if dialog === 'speech-admin'}
          <SpeechAdminSettings
            locale={$locale}
            on:changed={() => {
              if (user) void refreshSpeechPreference();
            }}
          />
        {:else}
          <div class="dialog-icon"><Icon name="sparkles" size={23} /></div>
          <h2 id="dialog-title">La4RainGPT</h2>
          <p class="dialog-lead">
            {t(
              '为三名受邀用户精简的私人 AI 聊天界面，由 Open WebUI 的设计与代码基础衍生。',
              'A streamlined private AI chat for three invited users, derived from the Open WebUI design and codebase.'
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
      if (workbenchPickerOpen || modelPickerOpen || effortPickerOpen) {
        workbenchPickerOpen = false;
        modelPickerOpen = false;
        effortPickerOpen = false;
        return;
      }
      if (dialog) {
        dialog = '';
        return;
      }
    }
    if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k' && phase === 'ready') {
      event.preventDefault();
      void startNewChat();
    }
  }}
/>
