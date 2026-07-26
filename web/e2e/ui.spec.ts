import { expect, test, type Page } from '@playwright/test';

const user = {
  id: 'user-1',
  username: 'alice',
  displayName: 'Alice',
  role: 'user',
  preferredModel: 'gpt-5.6-sol',
  createdAt: Date.now(),
  updatedAt: Date.now()
};

const model = {
  id: 'gpt-5.6-sol',
  name: 'GPT 5.6 Sol',
  description: 'Latest frontier agentic coding model.',
  contextWindow: 200000,
  inputModalities: ['text', 'image'],
  supportsWebSearch: true,
  imageGenerationMode: 'responses_tool',
  reasoningEfforts: ['medium', 'high', 'max'],
  defaultReasoningEffort: 'high',
  capabilitiesComplete: true,
  selectable: true
};

const models = [
  {
    ...model,
    id: 'gpt-5.6-luna',
    name: 'GPT 5.6 Luna',
    description: 'Fast model for everyday work.'
  },
  {
    ...model,
    id: 'gpt-5.6-terra',
    name: 'GPT 5.6 Terra',
    description: 'Balanced model for most tasks.'
  },
  model,
  {
    ...model,
    id: 'grok-4.5',
    name: 'Grok 4.5',
    description: 'A selectable non-GPT model that must stay hidden.'
  }
];

const onePixelPNG =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=';

async function mockAPI(page: Page) {
  let loggedIn = false;
  let conversationCreated = false;
  let conversationReasoningEffort = 'high';
  let checkpointReady = false;
  let speechMode: 'manual' | 'auto' = 'manual';
  let speechSpeed = 1;
  let speechVoice = '';
  const speechTexts: string[] = [];
  const speechVoices = [
    { id: 'zh_female_vv_uranus_bigtts', label: 'Vivi 2.0（女声·中英）' },
    { id: 'en_male_tim_uranus_bigtts', label: 'Tim（男声·英文）' }
  ];
  await page.routeWebSocket('**/api/v1/speech/sessions', (socket) => {
    setTimeout(() => {
      socket.send(JSON.stringify({ type: 'speech.connecting', provider: 'volcengine' }));
      socket.send(JSON.stringify({
        type: 'speech.started',
        provider: 'volcengine',
        voice: speechVoice || speechVoices[0].id,
        speed: speechSpeed,
        audio: { format: 'pcm', sampleRate: 24000, channels: 1, bitDepth: 16 }
      }));
    }, 10);
    socket.onMessage((raw) => {
      if (typeof raw !== 'string') return;
      const message = JSON.parse(raw) as { type: string; text?: string };
      if (message.type === 'speech.text') {
        speechTexts.push(message.text || '');
        socket.send(Buffer.alloc(24000 * 2 * 3));
      }
      if (message.type === 'speech.finish') {
        socket.send(JSON.stringify({ type: 'speech.completed', textBytes: 128 }));
      }
    });
  });
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname;
    const json = (status: number, body: unknown) =>
      route.fulfill({
        status,
        contentType: 'application/json',
        body: JSON.stringify(body)
      });

    if (path === '/api/v1/me') {
      return loggedIn
        ? json(200, { user, csrfToken: 'csrf-test' })
        : json(401, { error: { code: 'unauthorized', message: 'Sign in.' } });
    }
    if (path === '/api/v1/auth/login') {
      loggedIn = true;
      return json(200, { user, csrfToken: 'csrf-test' });
    }
    if (path === '/api/v1/models') return json(200, { models });
    if (path === '/api/v1/me/speech') {
      if (request.method() === 'PUT') {
        const body = request.postDataJSON() as {
          mode: 'manual' | 'auto';
          speed: number;
          voice: string;
        };
        speechMode = body.mode;
        speechSpeed = body.speed;
        speechVoice = body.voice;
      }
      return json(200, {
        speech: {
          mode: speechMode,
          autoRead: speechMode === 'auto',
          speed: speechSpeed,
          voice: speechVoice,
          effectiveVoice: speechVoice || speechVoices[0].id,
          updatedAt: Date.now(),
          serviceEnabled: true,
          provider: 'volcengine',
          providerConfigured: true,
          voices: speechVoices,
          audioAuthorization: 'required_on_each_device'
        }
      });
    }
    if (path === '/api/v1/me/storage') {
      return json(200, {
        storage: {
          usedBytes: 64 * 1024 * 1024,
          limitBytes: 3 * 1024 * 1024 * 1024,
          retainedBytes: 12 * 1024 * 1024,
          activeConversations: conversationCreated ? 1 : 0,
          maxActiveConversations: 30,
          pinnedConversations: 0,
          maxPinnedConversations: 10,
          retentionDays: 7
        }
      });
    }
    if (path === '/api/v1/attachments/generated-1/content') {
      return route.fulfill({
        status: 200,
        contentType: 'image/png',
        body: Buffer.from(onePixelPNG, 'base64')
      });
    }
    if (path === '/api/v1/conversations' && request.method() === 'GET') {
      return json(200, {
        conversations: conversationCreated
          ? [
              {
                id: 'conversation-1',
                ownerId: user.id,
                title: 'New chat',
                model: model.id,
                reasoningEffort: conversationReasoningEffort,
                createdAt: Date.now(),
                updatedAt: Date.now()
              }
            ]
          : []
      });
    }
    if (path === '/api/v1/conversations' && request.method() === 'POST') {
      const body = request.postDataJSON() as {
        model: string;
        reasoningEffort: string;
      };
      expect(body.model).toBe(model.id);
      expect(body.reasoningEffort).toBe('medium');
      conversationCreated = true;
      conversationReasoningEffort = body.reasoningEffort;
      return json(201, {
        conversation: {
          id: 'conversation-1',
          title: 'New chat',
          model: model.id,
          reasoningEffort: body.reasoningEffort,
          createdAt: Date.now(),
          updatedAt: Date.now()
        }
      });
    }
    if (path === '/api/v1/conversations/conversation-1' && request.method() === 'PATCH') {
      const body = request.postDataJSON() as {
        title?: string;
        reasoningEffort?: string;
        pinned?: boolean;
      };
      if (body.reasoningEffort) conversationReasoningEffort = body.reasoningEffort;
      return json(200, {
        conversation: {
          id: 'conversation-1',
          ownerId: user.id,
          title: body.title || 'New chat',
          model: model.id,
          reasoningEffort: conversationReasoningEffort,
          pinnedAt: body.pinned ? Date.now() : undefined,
          createdAt: Date.now(),
          updatedAt: Date.now()
        }
      });
    }
    if (
      path === '/api/v1/conversations/conversation-1/context-checkpoints' &&
      request.method() === 'GET'
    ) {
      return json(200, {
        checkpoints: checkpointReady
          ? [
              {
                id: 'checkpoint-1',
                conversationId: 'conversation-1',
                boundaryMessageId: 'message-user',
                model: model.id,
                summaryText:
                  '## 长期用户偏好\n无\n## 当前话题状态\n正在总结科技新闻',
                sourceFirstMessageId: 'message-user',
                sourceLastMessageId: 'message-user',
                estimatedTokensBefore: 160000,
                estimatedTokensAfter: 92000,
                sourceBytes: 4096,
                status: 'completed',
                createdAt: Date.now()
              }
            ]
          : []
      });
    }
    if (path.endsWith('/responses') && request.method() === 'POST' && conversationCreated) {
      const body = request.postDataJSON() as { generateImage: boolean };
      expect(body.generateImage).toBe(true);
      await new Promise((resolve) => setTimeout(resolve, 1700));
      checkpointReady = true;
      const now = Date.now();
      const responseText =
        '### 今日科技摘要\n\n重点是 **AI 基础设施** 与端侧模型。公式示例：$E = mc^2$。\n\n' +
        '| 项目 | 中国来源 | 发布日期 | 核心结论 | 适用人群 | 风险提示 | 后续动作 |\n' +
        '| --- | --- | --- | --- | --- | --- | --- |\n' +
        '| 端侧模型 | 官方技术博客 | 2026-07-25 | 推理效率继续提升 | 移动开发者 | 留意设备兼容性 | 核对正式文档 |\n' +
        '| AI 基础设施 | 行业协会报告 | 2026-07-24 | 算力调度成为重点 | 平台团队 | 样本口径不同 | 对照原始数据 |\n\n' +
        '> 重要结论需要回到原始来源核对，尤其要区分官方信息与用户评价。\n\n' +
        '另有政府公开材料把“老当湖”对应到“平湖市老厚润餐饮管理有限公司”。（pinghu.gov.cn）\n\n' +
        '详细规则参见[火山引擎音色文档](https://www.volcengine.com/docs/6561/1257544)。\n\n' +
        '长链接测试：https://example.com/research/2026/a-very-long-path-that-must-not-break-the-mobile-message-layout?source=official&language=zh-CN\n\n' +
        '```ts\nconst answer = 42;\n```\n\n<span onmouseover="window.__owuiXSS = true">安全渲染</span><script>window.__owuiXSS = true</script>\n\n' +
        Array.from(
          { length: 28 },
          (_, index) => `补充说明 ${index + 1}：这是用于验证长回答滚动行为的内容。`
        ).join('\n\n');
      const userMessage = {
        id: 'message-user',
        conversationId: 'conversation-1',
        role: 'user',
        status: 'completed',
        createdAt: now,
        parts: [{ type: 'text', text: '搜索并总结今天值得关注的科技新闻' }]
      };
      const assistant = {
        id: 'message-assistant',
        conversationId: 'conversation-1',
        role: 'assistant',
        model: model.id,
        reasoningEffortRequested: 'medium',
        reasoningEffortSent: 'medium',
        status: 'streaming',
        parentMessageId: 'message-user',
        createdAt: now + 1,
        parts: []
      };
      const final = {
        ...assistant,
        status: 'completed',
        outputTokens: 128,
        parts: [
          {
            type: 'reasoning',
            text: '我会先检索可靠来源，再归纳共同趋势。',
            data: {
              durationMs: 1200,
              providerItemId: 'reasoning-1',
              outputIndex: 0,
              summaryIndex: 0,
              completed: true
            }
          },
          {
            type: 'reasoning',
            text: '我核对了来源时间，并准备组织最终回答。',
            data: {
              durationMs: 760,
              providerItemId: 'reasoning-1',
              outputIndex: 0,
              summaryIndex: 1,
              completed: true
            }
          },
          {
            type: 'tool',
            data: {
              callId: 'search-1',
              type: 'web_search',
              status: 'completed',
              durationMs: 842,
              data: { query: 'today technology news' }
            }
          },
          {
            type: 'text',
            text: responseText
          },
          {
            type: 'citations',
            data: {
              citations: [{ url: 'https://example.com/news', title: 'Example Technology' }]
            }
          },
          {
            type: 'tool',
            data: {
              callId: 'image-1',
              type: 'image_generation',
              status: 'completed',
              durationMs: 2100
            }
          },
          { type: 'image', attachmentId: 'generated-1' }
        ]
      };
      const sse = [
        ['response.queued', { position: 1, timeoutSeconds: 60 }],
        ['response.started', { requestId: 'request-1', userMessage, assistantMessage: assistant }],
        ['response.stage', { stage: 'preparing_context' }],
        [
          'response.context',
          {
            status: 'completed',
            checkpointId: 'checkpoint-1',
            boundaryMessageId: 'message-user'
          }
        ],
        [
          'response.reasoning.delta',
          {
            itemId: 'reasoning-1',
            outputIndex: 0,
            summaryIndex: 0,
            delta: '我会先检索可靠来源，再归纳共同趋势。'
          }
        ],
        [
          'response.reasoning.done',
          {
            itemId: 'reasoning-1',
            outputIndex: 0,
            summaryIndex: 0,
            text: '我会先检索可靠来源，再归纳共同趋势。',
            durationMs: 1200,
            completed: true
          }
        ],
        [
          'response.reasoning.delta',
          {
            itemId: 'reasoning-1',
            outputIndex: 0,
            summaryIndex: 1,
            delta: '我核对了来源时间，并准备组织最终回答。'
          }
        ],
        [
          'response.reasoning.done',
          {
            itemId: 'reasoning-1',
            outputIndex: 0,
            summaryIndex: 1,
            text: '我核对了来源时间，并准备组织最终回答。',
            durationMs: 760,
            completed: true
          }
        ],
        ['response.stage', { stage: 'searching' }],
        [
          'response.tool',
          {
            callId: 'search-1',
            type: 'web_search',
            status: 'in_progress',
            data: { query: 'today technology news' }
          }
        ],
        [
          'response.tool',
          {
            callId: 'search-1',
            type: 'web_search',
            status: 'completed',
            durationMs: 842,
            data: { query: 'today technology news' }
          }
        ],
        [
          'response.text.delta',
          {
            delta: responseText
          }
        ],
        [
          'response.tool',
          {
            callId: 'image-1',
            type: 'image_generation',
            status: 'completed',
            durationMs: 2100
          }
        ],
        [
          'response.image',
          { attachmentId: 'generated-1', url: '/api/v1/attachments/generated-1/content' }
        ],
        ['response.completed', { message: final }]
      ]
        .map(([event, data]) => `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`)
        .join('');
      return route.fulfill({
        status: 200,
        contentType: 'text/event-stream',
        headers: { 'Cache-Control': 'no-cache' },
        body: sse
      });
    }
    return json(404, { error: { code: 'not_found', message: 'Not found.' } });
  });
  return { speechTexts };
}

test('login and streaming chat are visually usable', async ({ page }, testInfo) => {
  const { speechTexts } = await mockAPI(page);
  await page.goto('/');
  await expect(page.getByRole('heading', { name: '欢迎回来' })).toBeVisible();
  if (testInfo.project.name === 'desktop') {
    await page.getByRole('button', { name: 'English' }).click();
    await expect(page.getByRole('heading', { name: 'Welcome back' })).toBeVisible();
    await page.getByRole('button', { name: '中文' }).click();
  }
  await page.screenshot({ path: testInfo.outputPath('login.png'), fullPage: true });

  await page.getByLabel('用户名').fill('alice');
  await page.getByLabel('密码', { exact: true }).fill('correct horse battery');
  await page.getByRole('checkbox', { name: /记住密码/ }).check();
  await page.getByRole('button', { name: '登录' }).click();
  await expect(page.getByRole('heading', { name: '今天想聊点什么？' })).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => localStorage.getItem('personal-chat-remember-username')))
    .toBe('alice');
  await expect(page.getByText('首次使用指南')).toBeVisible();
  await expect(page.getByRole('heading', { name: '选择快速、均衡或专家模式' })).toBeVisible();
  await page.getByRole('button', { name: '下一步' }).click();
  await expect(page.getByRole('heading', { name: '再决定需要思考多深' })).toBeVisible();
  await page.getByRole('button', { name: '下一步' }).click();
  await expect(page.getByRole('heading', { name: '把重要对话整理好' })).toBeVisible();
  await page.getByRole('button', { name: '下一步' }).click();
  await expect(page.getByRole('heading', { name: '从输入框开始，过程随时可查' })).toBeVisible();
  await page.getByRole('button', { name: '开始聊天' }).click();
  await expect(page.getByText('首次使用指南')).toHaveCount(0);
  await expect
    .poll(() =>
      page.evaluate(() => localStorage.getItem('personal-chat-onboarding-v1:user-1'))
    )
    .toBe('complete');
  const updateDialog = page.getByRole('dialog', { name: '让回答为你读出来' });
  await expect(updateDialog).toBeVisible();
  await expect(updateDialog).toContainText('头像菜单');
  await expect(updateDialog).toContainText('语音与朗读');
  await expect(updateDialog).toContainText('自动朗读');
  await expect(updateDialog).toContainText('默认音色与语速');
  await expect(updateDialog).toContainText('Agent 回答下方');
  await expect(updateDialog).toContainText('网页链接会继续显示在回答中');
  await page.screenshot({
    path: testInfo.outputPath('update-announcement.png'),
    fullPage: true
  });
  if (testInfo.project.name === 'mobile') {
    const updateBounds = await updateDialog.boundingBox();
    expect(updateBounds).not.toBeNull();
    expect(updateBounds!.x).toBeGreaterThanOrEqual(0);
    expect(updateBounds!.x + updateBounds!.width).toBeLessThanOrEqual(376);
    expect(updateBounds!.y + updateBounds!.height).toBeLessThanOrEqual(813);
  }
  await updateDialog.getByRole('button', { name: '知道了' }).click();
  await expect(updateDialog).toHaveCount(0);
  await expect
    .poll(() =>
      page.evaluate(() => localStorage.getItem('personal-chat-update-tts-v1:user-1'))
    )
    .toBe('complete');

  if (testInfo.project.name === 'mobile') {
    await page.getByRole('button', { name: '打开侧边栏' }).click();
  }
  await page.getByRole('button', { name: /Alice/ }).click();
  await page.getByRole('button', { name: '显示与字体' }).click();
  await expect(page.getByRole('heading', { name: '显示与字体' })).toBeVisible();
  await page.getByRole('radio', { name: /^较大/ }).click();
  await expect
    .poll(() => page.evaluate(() => document.documentElement.dataset.fontSize))
    .toBe('large');
  await expect
    .poll(() => page.evaluate(() => getComputedStyle(document.documentElement).fontSize))
    .toBe('18px');
  await page.getByRole('radio', { name: /^标准/ }).click();
  await page
    .getByRole('dialog', { name: '显示与字体' })
    .getByRole('button', { name: '关闭', exact: true })
    .click();
  await page.getByRole('button', { name: /Alice/ }).click();
  await page.getByRole('button', { name: '语音与朗读' }).click();
  await expect(page.getByRole('heading', { name: '语音与朗读' })).toBeVisible();
  await expect(page.getByRole('switch', { name: /自动朗读/ })).toHaveAttribute(
    'aria-checked',
    'false'
  );
  await expect(page.getByRole('option', { name: 'Vivi 2.0（女声·中英）' })).toBeAttached();
  await expect(page.getByRole('option', { name: 'Tim（男声·英文）' })).toBeAttached();
  await expect(page.getByRole('button', { name: '试听当前音色' })).toBeVisible();
  await page
    .getByRole('dialog', { name: '语音与朗读' })
    .getByRole('button', { name: '关闭', exact: true })
    .click();
  if (testInfo.project.name === 'mobile') {
    await page.setViewportSize({ width: 812, height: 375 });
    await page.getByRole('button', { name: /Alice/ }).click();
    await expect(page.getByRole('button', { name: '显示与字体' })).toBeVisible();
    await expect(page.getByRole('button', { name: '刷新应用' })).toBeVisible();
    const profileMenu = await page.locator('.profile-menu').boundingBox();
    expect(profileMenu).not.toBeNull();
    expect(profileMenu!.y).toBeGreaterThanOrEqual(0);
    expect(profileMenu!.y + profileMenu!.height).toBeLessThanOrEqual(376);
    await page.getByRole('button', { name: /Alice/ }).click();
    await page.setViewportSize({ width: 375, height: 812 });
    await page.locator('.mobile-close').click();
  }

  const initialSuggestions = await page.locator('.suggestion-grid strong').allTextContents();
  expect(initialSuggestions).toHaveLength(3);
  await page.getByRole('button', { name: '换一批' }).click();
  const refreshedSuggestions = await page.locator('.suggestion-grid strong').allTextContents();
  expect(refreshedSuggestions).toHaveLength(3);
  expect(refreshedSuggestions.some((value) => initialSuggestions.includes(value))).toBe(false);

  if (testInfo.project.name === 'desktop') {
    await page.getByRole('button', { name: /Alice/ }).click();
    await page.getByRole('button', { name: '新手指南' }).click();
    await expect(page.getByText('首次使用指南')).toBeVisible();
    await page.getByRole('button', { name: '跳过', exact: true }).click();

    await page.getByRole('button', { name: /Alice/ }).click();
    await page.getByRole('button', { name: '更新公告' }).click();
    await expect(page.getByRole('heading', { name: '让回答为你读出来' })).toBeVisible();
    await page.getByRole('button', { name: '知道了' }).click();

    await page.locator('.model-picker-trigger').click();
    await expect(page.getByRole('option')).toHaveCount(3);
    await expect(page.getByRole('option', { name: /^快速/ }))
      .toContainText('响应最快的模型，适合日常问答、改写和快速检索。');
    await expect(page.getByRole('option', { name: /^均衡/ }))
      .toContainText('智能与速度均衡的模型，适合大多数分析、写作和多步骤任务。');
    await expect(page.getByRole('option', { name: /^专家/ }))
      .toContainText('最高智能的模型，适合复杂推理、编程和高要求任务。');
    await expect(page.getByRole('option', { name: /^快速/ })).toContainText('GPT · Luna');
    await expect(page.getByRole('option', { name: /^均衡/ })).toContainText('GPT · Terra');
    await expect(page.getByRole('option', { name: /^专家/ })).toContainText('GPT · Sol');
    await expect(page.getByText('Grok 4.5')).toHaveCount(0);
    await page.getByRole('option', { name: /^专家/ }).click();

    await page.getByRole('button', { name: /Alice/ }).click();
    await page.getByRole('button', { name: '关于' }).click();
    await expect(page.getByRole('heading', { name: 'La4RainGPT' })).toBeVisible();
    await expect(page.getByText('它不是端到端加密服务')).toBeVisible();
    await page.getByRole('button', { name: '关闭', exact: true }).click();

    await page.getByRole('button', { name: /Alice/ }).click();
    await page.getByRole('button', { name: '账户与安全' }).click();
    await expect(page.getByRole('heading', { name: '账户与安全' })).toBeVisible();
    await expect(page.getByLabel('新密码', { exact: true })).toHaveAttribute('minlength', '6');
    await page.getByRole('button', { name: '关闭', exact: true }).click();

    await page.getByRole('button', { name: /Alice/ }).click();
    await page.getByRole('button', { name: 'English' }).click();
    await expect(page.getByRole('heading', { name: 'What would you like to talk about?' })).toBeVisible();
    await expect
      .poll(() => page.evaluate(() => document.documentElement.lang))
      .toBe('en');
    await page.getByRole('button', { name: /Alice/ }).click();
    await page.getByRole('button', { name: '中文' }).click();
  }

  if (testInfo.project.name === 'mobile') {
    await page.getByRole('button', { name: '打开侧边栏' }).click();
  }
  await page.getByRole('button', { name: '新对话' }).click();
  await expect(page.locator('.conversation-item')).toHaveCount(0);
  if (testInfo.project.name === 'mobile') {
    await page.getByRole('button', { name: '打开侧边栏' }).click();
  }
  await page.getByRole('button', { name: '新对话' }).click();
  await expect(page.locator('.conversation-item')).toHaveCount(0);

  await page.getByRole('button', { name: '推理强度' }).click();
  await page.getByRole('option', { name: /^低/ }).click();
  await expect(page.getByRole('button', { name: '推理强度' })).toContainText('推理 · 低');
  await page.getByLabel('聊天消息').fill('请生成一张未来城市的概念图');
  await page.locator('.image-mode-button').click();
  await expect(page.locator('.image-mode-button')).toHaveAttribute('aria-pressed', 'true');
  await page.getByRole('button', { name: '发送' }).click();
  await expect(page.locator('.conversation-item')).toHaveCount(1);
  await expect(page.locator('.context-status')).toContainText('正在发送请求');
  await expect(page.locator('.context-status')).toContainText('1 s', { timeout: 2500 });
  await expect(page.getByText('今日科技摘要')).toBeVisible();
  await expect(page.locator('.reasoning')).toHaveCount(2);
  await expect(page.locator('.reasoning').nth(0).locator('summary')).toContainText('第 1 段');
  await expect(page.locator('.reasoning').nth(0).locator('summary')).toContainText('1.2 秒');
  await expect(page.locator('.reasoning').nth(1).locator('summary')).toContainText('第 2 段');
  await expect(page.locator('.reasoning').nth(1)).toContainText(
    '我核对了来源时间，并准备组织最终回答。'
  );
  await expect(page.getByText('网页搜索')).toBeVisible();
  await expect(page.getByText('搜索内容')).toBeVisible();
  await expect(page.getByText('today technology news')).toBeVisible();
  await expect(page.getByText('图像生成')).toBeVisible();
  await expect(page.locator('img.message-image')).toBeVisible();
  await expect(page.getByRole('button', { name: '朗读' })).toBeVisible();
  await page.getByRole('button', { name: '朗读' }).click();
  const speechPlayer = page.getByLabel('朗读播放器');
  await expect(speechPlayer).toBeVisible();
  await expect(speechPlayer.getByRole('slider', { name: '朗读进度' })).toBeEnabled();
  await expect(speechPlayer.getByRole('combobox', { name: '朗读倍速' })).toHaveValue('1');
  await expect(speechPlayer.getByRole('button', { name: '停止并关闭朗读' })).toBeVisible();
  await expect.poll(() => speechTexts.join('')).not.toBe('');
  expect(speechTexts.join('')).toContain('平湖市老厚润餐饮管理有限公司');
  expect(speechTexts.join('')).toContain('火山引擎音色文档');
  expect(speechTexts.join('')).not.toContain('pinghu.gov.cn');
  expect(speechTexts.join('')).not.toContain('https://');
  await page.screenshot({ path: testInfo.outputPath('speech-player.png'), fullPage: true });
  await speechPlayer.getByRole('button', { name: '停止并关闭朗读' }).click();
  await expect(speechPlayer).toHaveCount(0);
  await expect
    .poll(() =>
      page.locator('img.message-image').evaluate((image: HTMLImageElement) => image.naturalWidth)
    )
    .toBeGreaterThan(0);
  await page.getByText('查看搜索来源').click();
  await expect(page.locator('.tool-source-list')).toContainText('Example Technology');
  await expect(page.locator('.citation-row')).toContainText('Example Technology');
  await expect(page.getByText('较早上下文已摘要')).toBeVisible();
  await expect(page.getByText('安全渲染')).toBeVisible();
  await expect(page.getByRole('region', { name: '可横向滚动的数据表' })).toBeVisible();
  await expect(page.locator('.markdown [onmouseover], .markdown script')).toHaveCount(0);
  expect(
    await page.evaluate(() => (window as typeof window & { __owuiXSS?: boolean }).__owuiXSS)
  ).toBeUndefined();
  if (testInfo.project.name === 'mobile') {
    const tableScroll = page.getByRole('region', { name: '可横向滚动的数据表' });
    await expect(page.getByText('表格可左右滑动，查看完整内容')).toBeVisible();
    expect(
      await tableScroll.evaluate((element) => element.scrollWidth > element.clientWidth)
    ).toBe(true);
    await tableScroll.evaluate((element) => {
      element.scrollLeft = 160;
    });
    expect(await tableScroll.evaluate((element) => element.scrollLeft)).toBeGreaterThan(0);

    const userBody = await page.locator('.message.user .message-body').boundingBox();
    const assistantBody = await page.locator('.message.assistant .message-body').boundingBox();
    const messageColumn = await page.locator('.message-column').boundingBox();
    expect(userBody).not.toBeNull();
    expect(assistantBody).not.toBeNull();
    expect(messageColumn).not.toBeNull();
    expect(userBody!.width).toBeLessThan(assistantBody!.width);
    expect(Math.abs(userBody!.x + userBody!.width - (assistantBody!.x + assistantBody!.width)))
      .toBeLessThanOrEqual(2);
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)
    ).toBe(true);
  }
  const feed = page.locator('.messages-scroll');
  await feed.evaluate((element) => {
    element.scrollTop = 0;
    element.dispatchEvent(new Event('scroll'));
  });
  await expect(page.getByRole('button', { name: '查看最新回答' })).toBeVisible();
  await page.getByRole('button', { name: '查看最新回答' }).click();
  await expect
    .poll(() =>
      feed.evaluate(
        (element) => element.scrollHeight - element.scrollTop - element.clientHeight
      )
    )
    .toBeLessThanOrEqual(2);
  await page.screenshot({ path: testInfo.outputPath('chat.png'), fullPage: true });
});

test('a persisted background response resumes after reopening its chat', async ({ page }) => {
  const startedAt = Date.now() - 6000;
  const conversation = {
    id: 'conversation-background',
    ownerId: user.id,
    title: '后台回答',
    model: model.id,
    reasoningEffort: 'high',
    createdAt: startedAt - 1000,
    updatedAt: startedAt
  };
  const userMessage = {
    id: 'message-background-user',
    conversationId: conversation.id,
    role: 'user',
    status: 'completed',
    createdAt: startedAt,
    parts: [{ type: 'text', text: '请在后台完成这个回答' }]
  };
  const runningMessage = {
    id: 'message-background-assistant',
    conversationId: conversation.id,
    role: 'assistant',
    model: model.id,
    reasoningEffortRequested: 'high',
    reasoningEffortSent: 'high',
    status: 'streaming',
    parentMessageId: userMessage.id,
    createdAt: startedAt + 1,
    parts: [{ type: 'text', text: '这是已经保存的部分回答。' }]
  };
  let responsePolls = 0;

  await page.addInitScript(() => {
    localStorage.setItem('personal-chat-onboarding-v1:user-1', 'complete');
    localStorage.setItem('personal-chat-update-tts-v1:user-1', 'complete');
  });
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    const respond = (body: unknown, status = 200) =>
      route.fulfill({
        status,
        contentType: 'application/json',
        body: JSON.stringify(body)
      });
    if (path === '/api/v1/me') {
      return respond({ user, csrfToken: 'csrf-background' });
    }
    if (path === '/api/v1/models') return respond({ models });
    if (path === '/api/v1/me/storage') {
      return respond({
        storage: {
          usedBytes: 0,
          limitBytes: 3 * 1024 * 1024 * 1024,
          retainedBytes: 0,
          activeConversations: 1,
          maxActiveConversations: 30,
          pinnedConversations: 0,
          maxPinnedConversations: 10,
          retentionDays: 7
        }
      });
    }
    if (path === '/api/v1/conversations') {
      return respond({ conversations: [conversation] });
    }
    if (path === `/api/v1/conversations/${conversation.id}/messages`) {
      return respond({ messages: [userMessage, runningMessage] });
    }
    if (path === `/api/v1/conversations/${conversation.id}/context-checkpoints`) {
      return respond({ checkpoints: [] });
    }
    if (path === `/api/v1/responses/${runningMessage.id}`) {
      responsePolls += 1;
      if (responsePolls < 3) {
        return respond({
          message: {
            ...runningMessage,
            parts: [{
              type: 'text',
              text: `这是已经保存的部分回答。后台进度 ${responsePolls}/2。`
            }]
          }
        });
      }
      return respond({
        message: {
          ...runningMessage,
          status: 'completed',
          completedAt: Date.now(),
          parts: [{ type: 'text', text: '后台回答已经完整保存。' }]
        }
      });
    }
    return respond({ error: { code: 'not_found', message: 'Not found.' } }, 404);
  });

  await page.goto('/');
  await expect(page.getByRole('heading', { name: '今天想聊点什么？' })).toBeVisible();
  if (page.viewportSize()!.width < 700) {
    await page.getByRole('button', { name: '打开侧边栏' }).click();
  }
  await page
    .locator('.conversation-item')
    .filter({ hasText: '后台回答' })
    .locator('.conversation-icon')
    .click();
  await expect(page.getByText('回答正在服务器后台继续生成')).toBeVisible();
  await expect(page.getByText(/这是已经保存的部分回答/)).toBeVisible();
  await expect(page.locator('.thinking-progress')).toContainText(/已运行 \d+ s/);
  await expect(page.getByText('后台回答已经完整保存。')).toBeVisible({
    timeout: 5000
  });
  await expect(page.getByText('回答正在服务器后台继续生成')).toHaveCount(0);
});

test('administrator can inspect another user chat and manage summary compatibility', async ({ page }, testInfo) => {
  const admin = {
    ...user,
    id: 'admin-1',
    username: 'admin',
    displayName: 'Administrator',
    role: 'admin'
  };
  const otherConversation = {
    id: 'conversation-other',
    ownerId: 'user-other',
    ownerUsername: 'rainsaa',
    ownerDisplayName: 'rainsaa',
    title: '旅行计划',
    model: model.id,
    reasoningEffort: 'high',
    createdAt: Date.now(),
    updatedAt: Date.now()
  };
  let summaryMode: 'auto' | 'off' = 'auto';
  let speechEnabled = true;
  await page.addInitScript(() => {
    localStorage.setItem('personal-chat-onboarding-v1:admin-1', 'complete');
    localStorage.setItem('personal-chat-update-tts-v1:admin-1', 'complete');
  });
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    const method = route.request().method();
    const respond = (body: unknown) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(body)
      });
    if (path === '/api/v1/me') return respond({ user: admin, csrfToken: 'admin-csrf' });
    if (path === '/api/v1/models') return respond({ models });
    if (path === '/api/v1/me/speech') {
      return respond({
        speech: {
          mode: 'manual',
          autoRead: false,
          speed: 1,
          voice: '',
          effectiveVoice: 'zh_female_tianmeitaozi_mars_bigtts',
          updatedAt: Date.now(),
          serviceEnabled: speechEnabled,
          provider: 'volcengine',
          providerConfigured: true,
          voices: [
            { id: 'zh_female_tianmeitaozi_mars_bigtts', label: '甜美桃子' }
          ],
          audioAuthorization: 'required_on_each_device'
        }
      });
    }
    if (path === '/api/v1/me/storage') {
      return respond({
        storage: {
          usedBytes: 0,
          limitBytes: 3 * 1024 * 1024 * 1024,
          retainedBytes: 0,
          activeConversations: 0,
          maxActiveConversations: 30,
          pinnedConversations: 0,
          maxPinnedConversations: 10,
          retentionDays: 7
        }
      });
    }
    if (path === '/api/v1/conversations') {
      return respond({ conversations: [otherConversation] });
    }
    if (path === '/api/v1/conversations/conversation-other/messages') {
      return respond({ messages: [] });
    }
    if (path === '/api/v1/conversations/conversation-other/context-checkpoints') {
      return respond({ checkpoints: [] });
    }
    if (path === '/api/v1/admin/progressive-summaries') {
      if (method === 'PUT') {
        summaryMode = (route.request().postDataJSON() as { mode: 'auto' | 'off' }).mode;
      }
      return respond({
        progressiveSummary: {
          mode: summaryMode,
          hardDisabled: false,
          effectiveState: summaryMode === 'off' ? 'disabled' : 'unknown',
          models: [],
          updatedAt: Date.now()
        }
      });
    }
    if (path === '/api/v1/admin/progressive-summaries/recheck') {
      return respond({
        progressiveSummary: {
          mode: summaryMode,
          hardDisabled: false,
          effectiveState: summaryMode === 'off' ? 'disabled' : 'unknown',
          models: [],
          updatedAt: Date.now()
        }
      });
    }
    if (path === '/api/v1/admin/speech') {
      if (method === 'PUT') {
        speechEnabled = Boolean(
          (route.request().postDataJSON() as { enabled: boolean }).enabled
        );
      }
      return respond({
        speech: {
          enabled: speechEnabled,
          provider: 'volcengine',
          defaultVoice: 'zh_female_tianmeitaozi_mars_bigtts',
          updatedAt: Date.now(),
          providers: [
            {
              id: 'aliyun',
              configured: false,
              voices: [{ id: 'longxiaochun', label: '龙小淳' }]
            },
            {
              id: 'volcengine',
              configured: true,
              voices: [
                { id: 'zh_female_tianmeitaozi_mars_bigtts', label: '甜美桃子' }
              ]
            }
          ],
          concurrency: { perUser: 1, global: 2 }
        }
      });
    }
    return route.fulfill({
      status: 404,
      contentType: 'application/json',
      body: JSON.stringify({ error: { code: 'not_found', message: 'Not found.' } })
    });
  });

  await page.goto('/');
  await expect(page.getByRole('heading', { name: '今天想聊点什么？' })).toBeVisible();
  await expect(page.getByText('管理员只读查看')).toHaveCount(0);
  if (testInfo.project.name === 'mobile') {
    await page.getByRole('button', { name: '打开侧边栏' }).click();
  }
  await page.getByRole('button', { name: '旅行计划' }).click();
  await expect(page.getByText('管理员只读查看')).toBeVisible();
  await expect(page.getByLabel('聊天消息')).toBeDisabled();
  await expect(page.getByRole('button', { name: '推理强度' })).toBeDisabled();
  await expect(page.locator('.conversation-actions')).toHaveCount(0);

  if (testInfo.project.name === 'mobile') {
    await page.getByRole('button', { name: '打开侧边栏' }).click();
  }
  await page.getByRole('button', { name: /Administrator/ }).click();
  await page.getByRole('button', { name: '推理摘要设置' }).click();
  await expect(page.getByRole('heading', { name: '渐进式推理摘要' })).toBeVisible();
  await expect(page.getByRole('radio', { name: /^自动/ })).toHaveAttribute(
    'aria-checked',
    'true'
  );
  await page.getByRole('radio', { name: /^关闭/ }).click();
  await page.getByRole('button', { name: '保存设置' }).click();
  await expect(page.getByText('设置已保存，只影响之后开始的新回答。')).toBeVisible();
  await page.getByRole('button', { name: '重新检测' }).click();
  await expect(page.getByText(/兼容状态已清除/)).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('admin-summary-settings.png'), fullPage: true });
  await page
    .getByRole('dialog', { name: '渐进式推理摘要' })
    .getByRole('button', { name: '关闭', exact: true })
    .click();
  await page.getByRole('button', { name: /Administrator/ }).click();
  await page.getByRole('button', { name: '语音服务设置' }).click();
  await expect(page.getByRole('heading', { name: '语音服务设置' })).toBeVisible();
  await expect(page.getByRole('switch', { name: '全局语音服务' })).toHaveAttribute(
    'aria-checked',
    'true'
  );
  await expect(page.getByText('API 凭据已通过安全文件配置')).toBeVisible();
  await expect(page.locator('.speech-admin-status small')).toContainText('每用户 1');
  await expect(page.locator('.speech-admin-status small')).toContainText('全应用 2');
  await page.getByRole('button', { name: '保存语音服务设置' }).click();
  await expect(page.getByText('设置已即时生效。')).toBeVisible();
  await page
    .getByRole('dialog', { name: '语音服务设置' })
    .getByRole('button', { name: '关闭', exact: true })
    .click();
  if (testInfo.project.name === 'mobile') {
    await page.locator('.mobile-close').click();
  }
});
