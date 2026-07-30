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

const dictationAvailability = {
  enabled: true,
  configured: true,
  provider: 'volcengine',
  maxDurationSeconds: 120,
  audioStored: false as const,
  updatedAt: Date.now()
};

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
        setTimeout(() => socket.close({ code: 1000, reason: 'completed' }), 0);
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
    if (path === '/api/v1/me/workbench') {
      return json(200, {
        workbench: { effective: 'general', initial: 'general' },
        guidanceEnabled: false
      });
    }
    if (path === '/api/v1/me/dictation') {
      return json(200, { dictation: dictationAvailability });
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
  // This scenario intentionally exercises onboarding, settings, model
  // selection, streaming, rich content, speech, and scrolling in one flow.
  // It can exceed Playwright's 30 s default on the 1 GiB validation VPS even
  // though each assertion is progressing normally.
  test.setTimeout(60_000);
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
  const updateDialog = page.getByRole('dialog', { name: '现在可以直接说话输入' });
  await expect(updateDialog).toBeVisible();
  await expect(updateDialog).toContainText('点击录音，也可按住说话');
  await expect(updateDialog).toContainText('搜索所有对话');
  await expect(updateDialog).toContainText('编辑并重新发送');
  await expect(updateDialog).toContainText('粘贴上传与自动草稿');
  await expect(updateDialog).toContainText('用量统计与主屏幕');
  await expect(updateDialog).toContainText('临时留档中的对话暂不参与');
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
      page.evaluate(() =>
        localStorage.getItem('personal-chat-update-voice-v1:user-1')
      )
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
  await page.getByRole('button', { name: '试听当前音色' }).click();
  const previewPlayer = page.getByLabel('朗读播放器');
  await expect(previewPlayer).toBeVisible();
  await expect(previewPlayer.getByRole('slider', { name: '朗读进度' })).toBeEnabled();
  await expect(previewPlayer).not.toContainText('朗读失败');
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
    await expect(
      page.getByRole('heading', { name: '现在可以直接说话输入' })
    ).toBeVisible();
    await page.getByRole('button', { name: '知道了' }).click();

    await page.locator('.model-picker-trigger').click();
    const chatModelList = page.getByRole('listbox', { name: '聊天模型' });
    await expect(chatModelList.getByRole('option')).toHaveCount(3);
    await expect(chatModelList.getByRole('option', { name: /^快速/ }))
      .toContainText('响应最快的模型，适合日常问答、改写和快速检索。');
    await expect(chatModelList.getByRole('option', { name: /^均衡/ }))
      .toContainText('智能与速度均衡的模型，适合大多数分析、写作和多步骤任务。');
    await expect(chatModelList.getByRole('option', { name: /^专家/ }))
      .toContainText('最高智能的模型，适合复杂推理、编程和高要求任务。');
    await expect(chatModelList.getByRole('option', { name: /^快速/ })).toContainText('GPT · Luna');
    await expect(chatModelList.getByRole('option', { name: /^均衡/ })).toContainText('GPT · Terra');
    await expect(chatModelList.getByRole('option', { name: /^专家/ })).toContainText('GPT · Sol');
    await expect(page.getByText('Grok 4.5')).toHaveCount(0);
    await chatModelList.getByRole('option', { name: /^专家/ }).click();

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

test('dictation supports click, hold, cancel, failure recovery, and manual send', async ({
  page
}, testInfo) => {
  test.setTimeout(45_000);
  const conversation = {
    id: 'conversation-dictation',
    ownerId: user.id,
    title: '语音输入测试',
    model: model.id,
    reasoningEffort: 'high',
    createdAt: Date.now() - 5000,
    updatedAt: Date.now()
  };
  const initialMessages = [
    {
      id: 'dictation-user-initial',
      conversationId: conversation.id,
      role: 'user',
      status: 'completed',
      createdAt: Date.now() - 4000,
      parts: [{ type: 'text', text: '先给我一个简短建议' }]
    },
    {
      id: 'dictation-assistant-initial',
      conversationId: conversation.id,
      role: 'assistant',
      model: model.id,
      status: 'completed',
      parentMessageId: 'dictation-user-initial',
      createdAt: Date.now() - 3000,
      parts: [{ type: 'text', text: '可以先从最重要的一件事开始。' }]
    }
  ];
  const dictationDrafts: string[] = [];
  const submittedTexts: string[] = [];
  let dictationSessionNumber = 0;
  let speechSessions = 0;
  let speechCancels = 0;
  let dictationCancels = 0;

  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.addInitScript((darkMode) => {
    localStorage.setItem('personal-chat-onboarding-v1:user-1', 'complete');
    localStorage.setItem('personal-chat-update-voice-v1:user-1', 'complete');
    localStorage.setItem('personal-chat-font-size', 'large');
    if (darkMode) localStorage.setItem('personal-chat-theme', 'dark');
  }, testInfo.project.name === 'desktop');
  await page.addInitScript(() => {
    // Keep the interaction test independent of a runner audio device. The
    // destination owns a real browser MediaStream audio track, so the
    // production Web Audio graph and recorder lifecycle still run unchanged.
    const contexts: AudioContext[] = [];
    Object.defineProperty(navigator.mediaDevices, 'getUserMedia', {
      configurable: true,
      value: async () => {
        const context = new AudioContext();
        contexts.push(context);
        return context.createMediaStreamDestination().stream;
      }
    });
  });
  await page.routeWebSocket('**/api/v1/speech/sessions', (socket) => {
    speechSessions += 1;
    setTimeout(() => {
      socket.send(JSON.stringify({
        type: 'speech.connecting',
        provider: 'volcengine'
      }));
      socket.send(JSON.stringify({
        type: 'speech.started',
        provider: 'volcengine',
        voice: 'zh_female_vv_uranus_bigtts',
        speed: 1,
        audio: {
          format: 'pcm',
          sampleRate: 24000,
          channels: 1,
          bitDepth: 16
        }
      }));
    }, 10);
    socket.onMessage((raw) => {
      if (typeof raw !== 'string') return;
      const message = JSON.parse(raw) as { type: string };
      if (message.type === 'speech.text') {
        socket.send(Buffer.alloc(24000 * 2 * 10));
      }
      if (message.type === 'speech.cancel') speechCancels += 1;
    });
  });
  await page.routeWebSocket('**/api/v1/dictation/sessions', (socket) => {
    dictationSessionNumber += 1;
    const sessionNumber = dictationSessionNumber;
    const partials: Record<number, string> = {
      1: '帮我设计十款',
      2: '这一段应该取消',
      3: '最后临时转写',
      4: '按住说话'
    };
    const finals: Record<number, string> = {
      1: '帮我设计十款特色煨汤',
      2: '这一段不应保留',
      4: '按住说话也可以'
    };
    setTimeout(() => {
      socket.send(JSON.stringify({ type: 'dictation.ready' }));
    }, 5);
    socket.onMessage((raw) => {
      if (typeof raw !== 'string') return;
      const message = JSON.parse(raw) as {
        type: string;
        draft?: string;
      };
      if (message.type === 'dictation.start') {
        dictationDrafts.push(message.draft || '');
        socket.send(JSON.stringify({ type: 'dictation.connecting' }));
        socket.send(JSON.stringify({
          type: 'dictation.started',
          maxDurationSeconds: 120,
          audio: {
            format: 'pcm',
            sampleRate: 16000,
            channels: 1,
            bitDepth: 16
          }
        }));
        setTimeout(() => {
          socket.send(JSON.stringify({
            type: 'dictation.transcript',
            text: partials[sessionNumber],
            definite: false
          }));
          if (sessionNumber === 3) {
            setTimeout(() => {
              socket.send(JSON.stringify({
                type: 'dictation.error',
                code: 'dictation_provider_busy',
                message: 'provider busy'
              }));
            }, 40);
          }
        }, 120);
      }
      if (message.type === 'dictation.finish') {
        socket.send(JSON.stringify({
          type: 'dictation.completed',
          text: finals[sessionNumber]
        }));
      }
      if (message.type === 'dictation.cancel') {
        dictationCancels += 1;
        socket.send(JSON.stringify({ type: 'dictation.cancelled' }));
      }
    });
  });
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const respond = (body: unknown, status = 200, contentType = 'application/json') =>
      route.fulfill({
        status,
        contentType,
        body:
          contentType === 'application/json'
            ? JSON.stringify(body)
            : String(body)
      });
    if (path === '/api/v1/me') {
      return respond({ user, csrfToken: 'csrf-dictation' });
    }
    if (path === '/api/v1/models') return respond({ models });
    if (path === '/api/v1/me/workbench') {
      return respond({
        workbench: { effective: 'general', initial: 'general' },
        guidanceEnabled: false
      });
    }
    if (path === '/api/v1/me/dictation') {
      return respond({ dictation: dictationAvailability });
    }
    if (path === '/api/v1/me/speech') {
      return respond({
        speech: {
          mode: 'manual',
          autoRead: false,
          speed: 1,
          voice: '',
          effectiveVoice: 'zh_female_vv_uranus_bigtts',
          updatedAt: Date.now(),
          serviceEnabled: true,
          provider: 'volcengine',
          providerConfigured: true,
          voices: [
            {
              id: 'zh_female_vv_uranus_bigtts',
              label: 'Vivi 2.0（女声·中英）'
            }
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
          activeConversations: 1,
          maxActiveConversations: 30,
          pinnedConversations: 0,
          maxPinnedConversations: 10,
          retentionDays: 7
        }
      });
    }
    if (path === '/api/v1/conversations' && request.method() === 'GET') {
      return respond({ conversations: [conversation] });
    }
    if (path === `/api/v1/conversations/${conversation.id}/messages`) {
      return respond({ messages: initialMessages });
    }
    if (path === `/api/v1/conversations/${conversation.id}/context-checkpoints`) {
      return respond({ checkpoints: [] });
    }
    if (
      path === `/api/v1/conversations/${conversation.id}/responses` &&
      request.method() === 'POST'
    ) {
      const body = request.postDataJSON() as { text: string };
      submittedTexts.push(body.text);
      const now = Date.now();
      const userMessage = {
        id: 'dictation-manual-user',
        conversationId: conversation.id,
        role: 'user',
        status: 'completed',
        createdAt: now,
        parts: [{ type: 'text', text: body.text }]
      };
      const assistantMessage = {
        id: 'dictation-manual-assistant',
        conversationId: conversation.id,
        role: 'assistant',
        model: model.id,
        status: 'streaming',
        parentMessageId: userMessage.id,
        createdAt: now + 1,
        parts: []
      };
      const finalMessage = {
        ...assistantMessage,
        status: 'completed',
        parts: [{ type: 'text', text: '已收到你确认后的语音转写。' }]
      };
      const sse = [
        [
          'response.started',
          {
            requestId: 'dictation-manual-request',
            userMessage,
            assistantMessage
          }
        ],
        ['response.completed', { message: finalMessage }]
      ]
        .map(([event, data]) =>
          `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`
        )
        .join('');
      return respond(sse, 200, 'text/event-stream');
    }
    return respond(
      { error: { code: 'not_found', message: 'Not found.' } },
      404
    );
  });

  await page.goto('/');
  await expect(page.getByRole('heading', { name: '今天想聊点什么？' })).toBeVisible();
  if (testInfo.project.name === 'mobile') {
    await page.getByRole('button', { name: '打开侧边栏' }).click();
  }
  await page
    .locator('.conversation-item')
    .filter({ hasText: conversation.title })
    .locator('.conversation-icon')
    .click();
  await expect(page.getByText('可以先从最重要的一件事开始。')).toBeVisible();

  await page.getByRole('button', { name: '朗读' }).click();
  await expect(page.getByLabel('朗读播放器')).toBeVisible();
  await expect.poll(() => speechSessions).toBe(1);

  const textarea = page.getByLabel('聊天消息');
  const microphone = page.locator('.dictation-button');
  await textarea.fill('原草稿');
  await microphone.click();
  await expect(page.locator('.dictation-status')).toContainText('正在听');
  const microphoneSize = await microphone.boundingBox();
  expect(microphoneSize).not.toBeNull();
  expect(microphoneSize!.width).toBeGreaterThanOrEqual(44);
  expect(microphoneSize!.height).toBeGreaterThanOrEqual(44);
  expect(
    await page.locator('.dictation-live-dot').evaluate((element) =>
      getComputedStyle(element, '::after').animationName
    )
  ).toBe('none');
  await expect(textarea).toBeDisabled();
  await expect(page.getByLabel('朗读播放器')).toHaveCount(0);
  await expect(page.getByRole('button', { name: '上传图片' })).toBeDisabled();
  await expect(page.locator('.image-mode-button')).toBeDisabled();
  await expect(page.locator('.speech-message-control').first()).toBeDisabled();
  await expect(textarea).toHaveValue('原草稿\n帮我设计十款');
  if (testInfo.project.name === 'mobile') {
    const statusBounds = await page.locator('.dictation-status').boundingBox();
    expect(statusBounds).not.toBeNull();
    expect(statusBounds!.x).toBeGreaterThanOrEqual(0);
    expect(statusBounds!.x + statusBounds!.width).toBeLessThanOrEqual(376);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth
      )
    ).toBe(true);
    await page.setViewportSize({ width: 812, height: 375 });
    await expect(page.locator('.dictation-status')).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth
      )
    ).toBe(true);
    await page.setViewportSize({ width: 375, height: 812 });
  }
  await microphone.click();
  await expect(textarea).toBeEnabled();
  await expect(textarea).toHaveValue('原草稿\n帮我设计十款特色煨汤');
  await expect.poll(() => speechCancels).toBe(1);
  await page.waitForTimeout(250);
  expect(speechSessions).toBe(1);
  expect(submittedTexts).toHaveLength(0);
  await page.getByRole('button', { name: '发送' }).click();
  await expect(page.getByText('已收到你确认后的语音转写。')).toBeVisible();
  expect(submittedTexts).toEqual(['原草稿\n帮我设计十款特色煨汤']);

  await textarea.fill('取消前草稿');
  await microphone.click();
  await expect(page.locator('.dictation-status')).toContainText('正在听');
  await expect(textarea).toHaveValue('取消前草稿\n这一段应该取消');
  await page.locator('.dictation-status').getByRole('button', { name: '取消' }).click();
  await expect(textarea).toBeEnabled();
  await expect(textarea).toHaveValue('取消前草稿');
  await expect.poll(() => dictationCancels).toBe(1);
  expect(submittedTexts).toHaveLength(1);

  await textarea.fill('失败前草稿');
  await microphone.click();
  await expect(page.locator('.dictation-status')).toContainText('正在听');
  await expect(page.locator('.workspace-error')).toContainText(
    '豆包语音识别暂时繁忙'
  );
  await expect(textarea).toBeEnabled();
  await expect(textarea).toHaveValue('失败前草稿\n最后临时转写');
  await page.locator('.workspace-error button').click();

  await textarea.fill('');
  const microphoneBounds = await microphone.boundingBox();
  expect(microphoneBounds).not.toBeNull();
  await page.mouse.move(
    microphoneBounds!.x + microphoneBounds!.width / 2,
    microphoneBounds!.y + microphoneBounds!.height / 2
  );
  await page.mouse.down();
  await expect(page.locator('.dictation-status')).toContainText('正在听');
  await page.waitForTimeout(650);
  await page.mouse.up();
  await expect(textarea).toBeEnabled();
  await expect(textarea).toHaveValue('按住说话也可以');
  expect(submittedTexts).toHaveLength(1);
  expect(dictationDrafts).toEqual([
    '原草稿',
    '取消前草稿',
    '失败前草稿',
    ''
  ]);
});

test('restaurant guidance choices update immediately and submit together', async ({ page }, testInfo) => {
  const restaurantUser = {
    ...user,
    id: 'restaurant-user',
    username: 'laochen',
    displayName: '老陈'
  };
  const conversation = {
    id: 'restaurant-conversation',
    ownerId: restaurantUser.id,
    title: '餐厅会员体系',
    model: model.id,
    reasoningEffort: 'high',
    createdAt: Date.now() - 5000,
    updatedAt: Date.now()
  };
  const clarificationMessage = {
    id: 'restaurant-card-message',
    conversationId: conversation.id,
    role: 'assistant',
    model: model.id,
    status: 'completed',
    parentMessageId: 'restaurant-user-message',
    createdAt: Date.now() - 3000,
    completedAt: Date.now() - 2000,
    parts: [
      {
        id: 'restaurant-card-part',
        sequence: 0,
        type: 'clarification',
        text: '需要补充会员体系的目标、客群和模块。',
        createdAt: Date.now() - 2000,
        data: {
          schemaVersion: 1,
          instanceId: 'restaurant-card-instance',
          round: 1,
          maxRounds: 3,
          intro: '先确认三个会明显影响方案方向的问题。',
          currentUnderstanding: ['需要设计餐厅会员与储值体系'],
          questions: [
            {
              key: 'goal',
              prompt: '首要目标是什么？',
              selection: 'single_select',
              options: [
                { key: 'repeat', label: '增加复购', description: '让顾客更愿意再次到店' },
                { key: 'cash', label: '回笼资金', description: '优先提升储值现金流' }
              ],
              allowOther: true,
              allowDelegatedDefault: true,
              minimumSelections: 1,
              maximumSelections: 1
            },
            {
              key: 'audience',
              prompt: '主要客群是谁？',
              selection: 'multi_select',
              options: [
                { key: 'family', label: '家庭聚餐', description: '以家庭和亲友聚餐为主' },
                { key: 'business', label: '商务宴请', description: '以商务接待为主' }
              ],
              allowOther: true,
              allowDelegatedDefault: true,
              minimumSelections: 1,
              maximumSelections: 1
            },
            {
              key: 'modules',
              prompt: '优先包含哪些模块？',
              selection: 'multi_select',
              options: [
                { key: 'stored_value', label: '储值规则', description: '充值档位与赠送额度' },
                { key: 'member_tier', label: '会员等级', description: '不同等级的权益设计' },
                { key: 'birthday', label: '生日权益', description: '生日月关怀与优惠' }
              ],
              allowOther: true,
              allowDelegatedDefault: true,
              minimumSelections: 1,
              maximumSelections: 2
            }
          ]
        }
      }
    ]
  };
  const roundTwoData = {
    schemaVersion: 1 as const,
    instanceId: 'restaurant-card-instance-round-2',
    round: 2,
    maxRounds: 3,
    intro: '再确认两个会影响会员规则落地方式的问题。',
    currentUnderstanding: [
      '首要目标是增加复购',
      '主要客群是家庭聚餐',
      '优先设计储值规则和会员等级'
    ],
    questions: [
      {
        key: 'visit_frequency',
        prompt: '顾客通常多久来一次？',
        selection: 'single_select' as const,
        options: [
          { key: 'weekly', label: '每周或每两周', description: '适合较高频的日常复购' },
          { key: 'monthly', label: '每月一两次', description: '适合家庭聚餐型顾客' }
        ],
        allowOther: true,
        allowDelegatedDefault: true,
        minimumSelections: 1,
        maximumSelections: 1
      },
      {
        key: 'benefit_boundary',
        prompt: '优惠力度希望偏向哪种？',
        selection: 'single_select' as const,
        options: [
          { key: 'conservative', label: '保守可持续', description: '避免长期依赖大折扣' },
          { key: 'balanced', label: '适中有吸引力', description: '兼顾吸引力和毛利空间' }
        ],
        allowOther: true,
        allowDelegatedDefault: true,
        minimumSelections: 1,
        maximumSelections: 1
      }
    ]
  };
  const roundThreeData = {
    schemaVersion: 1 as const,
    instanceId: 'restaurant-card-instance-round-3',
    round: 3,
    maxRounds: 3,
    intro: '最后确认两个会影响执行细节的问题。',
    currentUnderstanding: [
      '首要目标是增加复购',
      '主要客群是家庭聚餐',
      '优惠力度以保守可持续为主'
    ],
    questions: [
      {
        key: 'launch_scope',
        prompt: '首批准备覆盖哪些顾客？',
        selection: 'single_select' as const,
        options: [
          { key: 'regulars', label: '先邀请老顾客', description: '先小范围验证规则' },
          { key: 'everyone', label: '面向全部顾客', description: '直接全面上线' }
        ],
        allowOther: true,
        allowDelegatedDefault: true,
        minimumSelections: 1,
        maximumSelections: 1
      },
      {
        key: 'staff_script',
        prompt: '是否需要员工介绍话术？',
        selection: 'single_select' as const,
        options: [
          { key: 'include', label: '需要简短话术', description: '方便员工统一介绍' },
          { key: 'skip', label: '暂时不需要', description: '先聚焦会员规则' }
        ],
        allowOther: true,
        allowDelegatedDefault: true,
        minimumSelections: 1,
        maximumSelections: 1
      }
    ]
  };
  const submittedGuidance: Array<Record<string, unknown>> = [];

  await page.addInitScript((userId) => {
    localStorage.setItem(`personal-chat-onboarding-v1:${userId}`, 'complete');
  }, restaurantUser.id);
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const json = (status: number, body: unknown) =>
      route.fulfill({
        status,
        contentType: 'application/json',
        body: JSON.stringify(body)
      });

    if (path === '/api/v1/me') {
      return json(200, { user: restaurantUser, csrfToken: 'csrf-restaurant' });
    }
    if (path === '/api/v1/models') return json(200, { models });
    if (path === '/api/v1/me/workbench') {
      return json(200, {
        workbench: {
          effective: 'restaurant',
          initial: 'restaurant',
          preference: 'restaurant'
        },
        guidanceEnabled: true
      });
    }
    if (path === '/api/v1/me/speech') {
      return json(200, {
        speech: {
          mode: 'manual',
          autoRead: false,
          speed: 1,
          voice: '',
          effectiveVoice: '',
          updatedAt: Date.now(),
          serviceEnabled: false,
          provider: '',
          providerConfigured: false,
          voices: [],
          audioAuthorization: 'not_required'
        }
      });
    }
    if (path === '/api/v1/me/dictation') {
      return json(200, { dictation: dictationAvailability });
    }
    if (path === '/api/v1/me/storage') {
      return json(200, {
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
    if (path === '/api/v1/conversations' && request.method() === 'GET') {
      return json(200, { conversations: [conversation] });
    }
    if (path === `/api/v1/conversations/${conversation.id}/messages`) {
      return json(200, {
        messages: [
          {
            id: 'restaurant-user-message',
            conversationId: conversation.id,
            role: 'user',
            status: 'completed',
            createdAt: Date.now() - 4000,
            parts: [
              {
                id: 'restaurant-user-part',
                sequence: 0,
                type: 'text',
                text: '帮我设计饭店的充卡和会员体系',
                createdAt: Date.now() - 4000
              }
            ]
          },
          clarificationMessage
        ]
      });
    }
    if (path === `/api/v1/conversations/${conversation.id}/context-checkpoints`) {
      return json(200, { checkpoints: [] });
    }
    if (
      path === `/api/v1/conversations/${conversation.id}/responses` &&
      request.method() === 'POST'
    ) {
      const body = request.postDataJSON() as {
        guidanceSubmission?: Record<string, unknown>;
      };
      if (body.guidanceSubmission) submittedGuidance.push(body.guidanceSubmission);
      const intent = String(body.guidanceSubmission?.intent || '');
      const responseNumber = submittedGuidance.length;
      const nextRound = responseNumber + 1;
      const continuing = intent === 'continue_refining';
      const nextRoundData = nextRound === 2 ? roundTwoData : roundThreeData;
      const now = Date.now();
      const userMessage = {
        id: `restaurant-submission-message-${responseNumber}`,
        conversationId: conversation.id,
        role: 'user',
        status: 'completed',
        createdAt: now,
        parts: [
          {
            id: `restaurant-submission-part-${responseNumber}`,
            sequence: 0,
            type: 'clarification_submission',
            text: '本轮需求补充已提交。',
            data: body.guidanceSubmission,
            createdAt: now
          }
        ]
      };
      const assistantMessage = {
        id: continuing
          ? `restaurant-card-message-round-${nextRound}`
          : 'restaurant-final-message',
        conversationId: conversation.id,
        role: 'assistant',
        model: model.id,
        status: 'streaming',
        parentMessageId: userMessage.id,
        createdAt: now + 1,
        parts: []
      };
      const finalMessage = continuing
        ? {
            ...assistantMessage,
            status: 'completed',
            completedAt: now + 2,
            parts: [
              {
                id: `restaurant-card-part-round-${nextRound}`,
                sequence: 0,
                type: 'clarification',
                text:
                  nextRound === 2
                    ? '再确认顾客频率和优惠边界。'
                    : '最后确认上线范围和员工话术。',
                data: nextRoundData,
                createdAt: now + 2
              }
            ]
          }
        : {
            ...assistantMessage,
            status: 'completed',
            completedAt: now + 2,
            parts: [
              {
                id: 'restaurant-final-part',
                sequence: 0,
                type: 'text',
                text: '已根据三轮选择生成餐厅会员体系方案。',
                createdAt: now + 2
              }
            ]
          };
      const events: Array<[string, unknown]> = [
        [
          'response.started',
          {
            requestId: `restaurant-guidance-request-${responseNumber}`,
            userMessage,
            assistantMessage
          }
        ]
      ];
      if (!continuing) {
        events.push([
          'response.text.delta',
          { delta: '已根据三轮选择生成餐厅会员体系方案。' }
        ]);
      }
      events.push(['response.completed', { message: finalMessage }]);
      const sse = events
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

  await page.goto('/');
  const announcement = page.getByRole('dialog', {
    name: '餐饮问题，可以先说个大概'
  });
  await expect(announcement).toBeVisible();
  await expect(announcement).toContainText('答案都可以直接点击');
  await expect(announcement).toContainText('餐厅档案无需专门维护');
  if (testInfo.project.name === 'mobile') {
    const bounds = await announcement.boundingBox();
    expect(bounds).not.toBeNull();
    expect(bounds!.x).toBeGreaterThanOrEqual(0);
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(376);
  }
  await announcement.getByRole('button', { name: '知道了' }).click();
  await expect
    .poll(() =>
      page.evaluate(
        (userId) =>
          localStorage.getItem(`personal-chat-update-restaurant-voice-v1:${userId}`),
        restaurantUser.id
      )
    )
    .toBe('complete');

  if (testInfo.project.name === 'mobile') {
    await page.getByRole('button', { name: '打开侧边栏' }).click();
  }
  await page.getByRole('button', { name: /餐厅会员体系/ }).click();

  const cards = page.getByRole('region', { name: '需求澄清' });
  const card = cards.first();
  const generateButton = card.getByRole('button', { name: '按当前选择直接生成' });
  await expect(card).toBeVisible();
  await expect(card).toContainText('第 1/3 轮');
  await expect(generateButton).toBeDisabled();

  const goalGroup = card.getByRole('radiogroup', { name: '首要目标是什么？' });
  const repeatChoice = goalGroup.getByRole('radio', { name: /增加复购/ });
  await repeatChoice.click();
  await expect(repeatChoice).toHaveAttribute('aria-checked', 'true');
  await expect(card.getByText('本轮选择')).toBeVisible();
  await expect(generateButton).toBeDisabled();

  const otherGoal = goalGroup.getByRole('radio', { name: /其他 \/ 我来说明/ });
  await otherGoal.click();
  const otherGoalInput = card.getByLabel('其他 / 我来说明').first();
  await otherGoalInput.fill('以社区家庭顾客为主');
  await expect(otherGoal).toHaveAttribute('aria-checked', 'true');
  await expect(repeatChoice).toHaveAttribute('aria-checked', 'false');
  await repeatChoice.click();
  await expect(repeatChoice).toHaveAttribute('aria-checked', 'true');
  await expect(otherGoal).toHaveAttribute('aria-checked', 'false');

  const audienceGroup = card.getByRole('group', { name: '主要客群是谁？' });
  const delegatedChoice = audienceGroup.getByRole('checkbox', { name: /你帮我决定/ });
  await delegatedChoice.click();
  await expect(delegatedChoice).toHaveAttribute('aria-checked', 'true');
  const familyChoice = audienceGroup.getByRole('checkbox', { name: /家庭聚餐/ });
  const otherAudience = audienceGroup.getByRole('checkbox', { name: /其他 \/ 我来说明/ });
  await expect(familyChoice).toBeEnabled();
  await expect(otherAudience).toBeEnabled();
  await otherAudience.click();
  await card.getByLabel('其他 / 我来说明').fill('以家庭聚餐顾客为主');
  await expect(otherAudience).toHaveAttribute('aria-checked', 'true');
  await expect(delegatedChoice).toHaveAttribute('aria-checked', 'false');
  await delegatedChoice.click();
  await expect(otherAudience).toHaveAttribute('aria-checked', 'false');
  await familyChoice.click();
  await expect(familyChoice).toHaveAttribute('aria-checked', 'true');
  await expect(delegatedChoice).toHaveAttribute('aria-checked', 'false');
  await familyChoice.click();
  await delegatedChoice.click();
  await expect(delegatedChoice).toHaveAttribute('aria-checked', 'true');

  const modulesGroup = card.getByRole('group', { name: '优先包含哪些模块？' });
  const storedValueChoice = modulesGroup.getByRole('checkbox', { name: /储值规则/ });
  const tierChoice = modulesGroup.getByRole('checkbox', { name: /会员等级/ });
  const birthdayChoice = modulesGroup.getByRole('checkbox', { name: /生日权益/ });
  await storedValueChoice.click();
  await tierChoice.click();
  await expect(storedValueChoice).toHaveAttribute('aria-checked', 'true');
  await expect(tierChoice).toHaveAttribute('aria-checked', 'true');
  await expect(birthdayChoice).toBeDisabled();
  await expect(generateButton).toBeEnabled();
  await expect(card.getByRole('button', { name: '继续完善' })).toBeEnabled();

  await expect
    .poll(() =>
      page.evaluate(() => {
        const raw = localStorage.getItem(
          'restaurant-guidance-draft-v1:restaurant-user:restaurant-conversation:restaurant-card-message'
        );
        return raw ? JSON.parse(raw) : null;
      })
    )
    .toMatchObject({
      goal: { selectedOptionKeys: ['repeat'] },
      audience: { delegatedDefault: true },
      modules: { selectedOptionKeys: ['stored_value', 'member_tier'] }
    });

  await familyChoice.click();
  await expect(familyChoice).toHaveAttribute('aria-checked', 'true');
  await expect(delegatedChoice).toHaveAttribute('aria-checked', 'false');
  await card.getByRole('button', { name: '继续完善' }).click();

  await expect(cards).toHaveCount(2);
  expect(submittedGuidance[0]).toMatchObject({
    sourceAssistantMessageId: clarificationMessage.id,
    sourcePartId: 'restaurant-card-part',
    intent: 'continue_refining',
    answers: [
      { questionKey: 'goal', selectedOptionKeys: ['repeat'] },
      {
        questionKey: 'audience',
        selectedOptionKeys: ['family']
      },
      {
        questionKey: 'modules',
        selectedOptionKeys: ['stored_value', 'member_tier']
      }
    ]
  });
  await expect(card).toContainText('本轮已提交或已被后续消息替代');
  await expect(repeatChoice).toHaveAttribute('aria-checked', 'true');
  await expect(familyChoice).toHaveAttribute('aria-checked', 'true');
  await expect(storedValueChoice).toHaveAttribute('aria-checked', 'true');
  await expect(tierChoice).toHaveAttribute('aria-checked', 'true');

  const roundTwoCard = cards.nth(1);
  await expect(roundTwoCard).toContainText('第 2/3 轮');
  await expect(roundTwoCard).toContainText('顾客通常多久来一次？');
  const monthlyChoice = roundTwoCard
    .getByRole('radiogroup', { name: '顾客通常多久来一次？' })
    .getByRole('radio', { name: /每月一两次/ });
  const conservativeChoice = roundTwoCard
    .getByRole('radiogroup', { name: '优惠力度希望偏向哪种？' })
    .getByRole('radio', { name: /保守可持续/ });
  await monthlyChoice.click();
  await conservativeChoice.click();
  await roundTwoCard.getByRole('button', { name: '继续完善' }).click();

  await expect(cards).toHaveCount(3);
  expect(submittedGuidance[1]).toMatchObject({
    sourceAssistantMessageId: 'restaurant-card-message-round-2',
    sourcePartId: 'restaurant-card-part-round-2',
    intent: 'continue_refining',
    answers: [
      { questionKey: 'visit_frequency', selectedOptionKeys: ['monthly'] },
      { questionKey: 'benefit_boundary', selectedOptionKeys: ['conservative'] }
    ]
  });
  await expect(roundTwoCard).toContainText('本轮已提交或已被后续消息替代');
  await expect(monthlyChoice).toHaveAttribute('aria-checked', 'true');
  await expect(conservativeChoice).toHaveAttribute('aria-checked', 'true');

  const roundThreeCard = cards.nth(2);
  await expect(roundThreeCard).toContainText('第 3/3 轮');
  await expect(
    roundThreeCard.getByRole('button', { name: '形成任务简报' })
  ).toBeDisabled();
  const regularsChoice = roundThreeCard
    .getByRole('radiogroup', { name: '首批准备覆盖哪些顾客？' })
    .getByRole('radio', { name: /先邀请老顾客/ });
  const includeScriptChoice = roundThreeCard
    .getByRole('radiogroup', { name: '是否需要员工介绍话术？' })
    .getByRole('radio', { name: /需要简短话术/ });
  await regularsChoice.click();
  await includeScriptChoice.click();
  await expect(
    roundThreeCard.getByRole('button', { name: '形成任务简报' })
  ).toBeEnabled();
  await roundThreeCard.getByRole('button', { name: '按当前选择直接生成' }).click();

  await expect(page.getByText('已根据三轮选择生成餐厅会员体系方案。')).toBeVisible();
  expect(submittedGuidance[2]).toMatchObject({
    sourceAssistantMessageId: 'restaurant-card-message-round-3',
    sourcePartId: 'restaurant-card-part-round-3',
    intent: 'generate_from_current',
    answers: [
      { questionKey: 'launch_scope', selectedOptionKeys: ['regulars'] },
      { questionKey: 'staff_script', selectedOptionKeys: ['include'] }
    ]
  });
  await expect(roundThreeCard).toContainText('本轮已提交或已被后续消息替代');
  await expect(regularsChoice).toHaveAttribute('aria-checked', 'true');
  await expect(includeScriptChoice).toHaveAttribute('aria-checked', 'true');
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
    localStorage.setItem('personal-chat-update-voice-v1:user-1', 'complete');
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
    if (path === '/api/v1/me/workbench') {
      return respond({
        workbench: { effective: 'general', initial: 'general' },
        guidanceEnabled: false
      });
    }
    if (path === '/api/v1/me/dictation') {
      return respond({ dictation: dictationAvailability });
    }
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

test('search, drafts, message editing, and usage stats work', async ({ page }, testInfo) => {
  const conversation = {
    id: 'conversation-bread',
    ownerId: user.id,
    title: '面包烘焙',
    model: model.id,
    reasoningEffort: 'high',
    createdAt: Date.now() - 5000,
    updatedAt: Date.now()
  };
  let userText = '如何烤出松软的全麦面包？';
  const originalAssistant = {
    id: 'message-assistant',
    conversationId: conversation.id,
    role: 'assistant',
    model: model.id,
    status: 'completed',
    parentMessageId: 'message-user',
    createdAt: Date.now() - 3000,
    outputTokens: 64,
    parts: [{ type: 'text', text: '先用中种法发酵。' }]
  };
  let conversationMessages = [
    {
      id: 'message-user',
      conversationId: conversation.id,
      role: 'user',
      status: 'completed',
      createdAt: Date.now() - 4000,
      parts: [{ type: 'text', text: userText }]
    },
    originalAssistant
  ];

  await page.addInitScript(() => {
    localStorage.setItem('personal-chat-onboarding-v1:user-1', 'complete');
    localStorage.setItem('personal-chat-update-voice-v1:user-1', 'complete');
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
    if (path === '/api/v1/me') return json(200, { user, csrfToken: 'csrf-search' });
    if (path === '/api/v1/models') return json(200, { models });
    if (path === '/api/v1/me/workbench') {
      return json(200, {
        workbench: { effective: 'general', initial: 'general' },
        guidanceEnabled: false
      });
    }
    if (path === '/api/v1/me/dictation') {
      return json(200, { dictation: dictationAvailability });
    }
    if (path === '/api/v1/me/storage') {
      return json(200, {
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
    if (path === '/api/v1/conversations' && request.method() === 'GET') {
      return json(200, { conversations: [conversation] });
    }
    if (path === `/api/v1/conversations/${conversation.id}/messages`) {
      return json(200, { messages: conversationMessages });
    }
    if (path === `/api/v1/conversations/${conversation.id}/context-checkpoints`) {
      return json(200, { checkpoints: [] });
    }
    if (path === '/api/v1/search') {
      const query = url.searchParams.get('q') || '';
      return json(200, {
        results: query.includes('全麦')
          ? [
              {
                conversation,
                snippet: '…如何烤出松软的全麦面包？…',
                matchedIn: 'message'
              }
            ]
          : []
      });
    }
    if (path === '/api/v1/usage') {
      return json(200, {
        usage: [
          {
            month: '2026-07',
            model: model.id,
            responses: 12,
            inputTokens: 84210,
            outputTokens: 12345,
            reasoningTokens: 2048
          }
        ]
      });
    }
    if (
      path === '/api/v1/messages/message-assistant-2/regenerate' &&
      request.method() === 'POST'
    ) {
      const now = Date.now();
      const assistant = {
        id: 'message-assistant-3',
        conversationId: conversation.id,
        role: 'assistant',
        model: model.id,
        status: 'streaming',
        parentMessageId: 'message-user',
        createdAt: now,
        parts: []
      };
      const final = {
        ...assistant,
        status: 'completed',
        outputTokens: 32,
        parts: [{ type: 'text', text: '再补充一个替代做法。' }]
      };
      conversationMessages = [...conversationMessages, final];
      const sse = [
        [
          'response.started',
          { requestId: 'regen-1', regenerated: true, assistantMessage: assistant }
        ],
        ['response.text.delta', { delta: '再补充一个替代做法。' }],
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
    if (path === '/api/v1/messages/message-user/edit' && request.method() === 'POST') {
      const body = request.postDataJSON() as { text: string };
      userText = body.text;
      const now = Date.now();
      const editedUser = {
        id: 'message-user',
        conversationId: conversation.id,
        role: 'user',
        status: 'completed',
        createdAt: now - 4000,
        parts: [{ type: 'text', text: body.text }]
      };
      const assistant = {
        id: 'message-assistant-2',
        conversationId: conversation.id,
        role: 'assistant',
        model: model.id,
        status: 'streaming',
        parentMessageId: 'message-user',
        createdAt: now,
        parts: []
      };
      const final = {
        ...assistant,
        status: 'completed',
        outputTokens: 48,
        parts: [{ type: 'text', text: '换成高筋粉再试一次。' }]
      };
      conversationMessages = [editedUser, originalAssistant, final];
      const sse = [
        [
          'response.started',
          { requestId: 'edit-1', userMessage: editedUser, assistantMessage: assistant }
        ],
        ['response.text.delta', { delta: '换成高筋粉再试一次。' }],
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

  await page.goto('/');
  await expect(page.getByRole('heading', { name: '今天想聊点什么？' })).toBeVisible();

  // A composer draft survives a reload.
  await page.getByLabel('聊天消息').fill('周末去哪里玩比较好？');
  await expect
    .poll(() =>
      page.evaluate(() => localStorage.getItem('personal-chat-draft:user-1:new'))
    )
    .toBe('周末去哪里玩比较好？');
  await page.reload();
  await expect(page.getByRole('heading', { name: '今天想聊点什么？' })).toBeVisible();
  await expect(page.getByLabel('聊天消息')).toHaveValue('周末去哪里玩比较好？');
  await page.getByLabel('聊天消息').fill('');

  // Sidebar search finds message text and opens the matching chat.
  if (testInfo.project.name === 'mobile') {
    await page.getByRole('button', { name: '打开侧边栏' }).click();
  }
  await page.getByLabel('搜索对话').fill('全麦');
  await expect(page.locator('.search-result')).toHaveCount(1);
  await expect(page.locator('.search-snippet')).toContainText('全麦面包');
  await page.locator('.search-result').click();
  await expect(page.getByText('先用中种法发酵。')).toBeVisible();

  // The latest user message can be edited and resent.
  await page.locator('.message.user').hover();
  await page.getByRole('button', { name: '编辑并重新发送' }).click();
  const editBox = page.getByLabel('编辑消息内容');
  await expect(editBox).toHaveValue('如何烤出松软的全麦面包？');
  await editBox.fill('如何烤出高纤维的全麦贝果？');
  await page.getByRole('button', { name: '重新发送' }).click();
  await expect(page.getByText('如何烤出高纤维的全麦贝果？')).toBeVisible();
  await expect(page.getByText('换成高筋粉再试一次。')).toBeVisible();
  await expect(page.getByText('先用中种法发酵。')).toBeVisible();

  // Regenerating the newest answer runs through the same shared stream runner.
  await page.locator('.message.assistant').last().hover();
  await page.getByRole('button', { name: '重新生成' }).click();
  await expect(page.getByText('再补充一个替代做法。')).toBeVisible();
  await expect(page.getByText('换成高筋粉再试一次。')).toBeVisible();

  // The usage dialog shows monthly aggregates.
  if (testInfo.project.name === 'mobile') {
    await page.getByRole('button', { name: '打开侧边栏' }).click();
  }
  await page.getByRole('button', { name: /Alice/ }).click();
  await page.getByRole('button', { name: '用量统计' }).click();
  const usageDialog = page.getByRole('dialog', { name: '用量统计' });
  await expect(usageDialog).toBeVisible();
  await expect(usageDialog).toContainText('2026 年 7 月');
  await expect(usageDialog).toContainText('专家');
  await expect(usageDialog).toContainText('12,345');
  await expect(usageDialog).toContainText('含推理 2,048');
  await usageDialog.getByRole('button', { name: '关闭', exact: true }).click();
  await expect(usageDialog).toHaveCount(0);
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
  let dictationEnabled = true;
  await page.addInitScript(() => {
    localStorage.setItem('personal-chat-onboarding-v1:admin-1', 'complete');
    localStorage.setItem('personal-chat-update-voice-v1:admin-1', 'complete');
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
    if (path === '/api/v1/me/dictation') {
      return respond({
        dictation: {
          ...dictationAvailability,
          enabled: dictationEnabled
        }
      });
    }
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
    if (path === '/api/v1/me/workbench') {
      return respond({
        workbench: { effective: 'general', initial: 'general' },
        guidanceEnabled: false
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
    if (path === '/api/v1/admin/dictation') {
      if (method === 'PUT') {
        dictationEnabled = Boolean(
          (route.request().postDataJSON() as { enabled: boolean }).enabled
        );
      }
      return respond({
        dictation: {
          ...dictationAvailability,
          enabled: dictationEnabled,
          resourceId: 'volc.seedasr.sauc.duration',
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
  await page.getByRole('button', { name: /Administrator/ }).click();
  await page.getByRole('button', { name: '语音输入设置' }).click();
  const dictationDialog = page.getByRole('dialog', { name: '语音输入设置' });
  await expect(dictationDialog).toBeVisible();
  await expect(
    dictationDialog.getByRole('switch', { name: '全局语音输入' })
  ).toHaveAttribute('aria-checked', 'true');
  await expect(dictationDialog).toContainText('豆包流式语音识别 2.0');
  await expect(dictationDialog).toContainText('麦克风原始音频不保存');
  await expect(dictationDialog.locator('.speech-admin-status small')).toContainText(
    '每用户 1'
  );
  await expect(dictationDialog.locator('.speech-admin-status small')).toContainText(
    '全应用 2'
  );
  await dictationDialog.getByRole('switch', { name: '全局语音输入' }).click();
  await dictationDialog.getByRole('button', { name: '保存语音输入设置' }).click();
  await expect(dictationDialog.getByText('设置已即时生效。')).toBeVisible();
  await dictationDialog.getByRole('button', { name: '关闭', exact: true }).click();
  if (testInfo.project.name === 'mobile') {
    await page.locator('.mobile-close').click();
  }
});
