import { expect, test, type Page } from '@playwright/test';

const user = {
  id: 'user-1',
  username: 'alice',
  displayName: 'Alice',
  role: 'user',
  preferredModel: 'gpt-personal',
  createdAt: Date.now(),
  updatedAt: Date.now()
};

const model = {
  id: 'gpt-personal',
  name: 'GPT Personal',
  description: 'Mock CPA model',
  contextWindow: 200000,
  inputModalities: ['text', 'image'],
  supportsWebSearch: true,
  imageGenerationMode: 'responses_tool',
  reasoningEfforts: ['medium', 'high', 'max'],
  defaultReasoningEffort: 'high',
  capabilitiesComplete: true,
  selectable: true
};

const onePixelPNG =
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=';

async function mockAPI(page: Page) {
  let loggedIn = false;
  let conversationCreated = false;
  let conversationReasoningEffort = 'high';
  let checkpointReady = false;
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
    if (path === '/api/v1/models') return json(200, { models: [model] });
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
      expect(body.reasoningEffort).toBe('high');
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
      checkpointReady = true;
      const now = Date.now();
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
            data: { durationMs: 1200 }
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
            text: '### 今日科技摘要\n\n重点是 **AI 基础设施** 与端侧模型。公式示例：$E = mc^2$。\n\n```ts\nconst answer = 42;\n```\n\n<span onmouseover="window.__owuiXSS = true">安全渲染</span><script>window.__owuiXSS = true</script>'
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
        [
          'response.context',
          {
            status: 'completed',
            checkpointId: 'checkpoint-1',
            boundaryMessageId: 'message-user'
          }
        ],
        ['response.reasoning.delta', { delta: '我会先检索可靠来源，再归纳共同趋势。' }],
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
            delta:
              '### 今日科技摘要\n\n重点是 **AI 基础设施** 与端侧模型。公式示例：$E = mc^2$。\n\n```ts\nconst answer = 42;\n```\n\n<span onmouseover="window.__owuiXSS = true">安全渲染</span><script>window.__owuiXSS = true</script>'
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
}

test('login and streaming chat are visually usable', async ({ page }, testInfo) => {
  await mockAPI(page);
  await page.goto('/');
  await expect(page.getByRole('heading', { name: '欢迎回来' })).toBeVisible();
  if (testInfo.project.name === 'desktop') {
    await page.getByRole('button', { name: 'English' }).click();
    await expect(page.getByRole('heading', { name: 'Welcome back' })).toBeVisible();
    await page.getByRole('button', { name: '中文' }).click();
  }
  await page.screenshot({ path: testInfo.outputPath('login.png'), fullPage: true });

  await page.getByLabel('用户名').fill('alice');
  await page.getByLabel('密码').fill('correct horse battery');
  await page.getByRole('button', { name: '登录' }).click();
  await expect(page.getByRole('heading', { name: '今天想聊点什么？' })).toBeVisible();

  if (testInfo.project.name === 'desktop') {
    await page.getByRole('button', { name: /GPT Personal/ }).click();
    await page.getByLabel('搜索模型').fill('gpt-personal');
    await expect(page.getByRole('option', { name: /GPT Personal/ })).toContainText(
      '图片输入 · 联网 · 图片生成 · 推理: 低/中/高'
    );
    await page.getByRole('option', { name: /GPT Personal/ }).click();

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
  await expect(page.locator('.conversation-item')).toHaveCount(1);
  if (testInfo.project.name === 'mobile') {
    await page.getByRole('button', { name: '打开侧边栏' }).click();
  }
  await page.getByRole('button', { name: '新对话' }).click();
  await expect(page.locator('.conversation-item')).toHaveCount(1);

  await page.getByRole('button', { name: '推理强度' }).click();
  await page.getByRole('option', { name: /^低/ }).click();
  await expect(page.getByRole('button', { name: '推理强度' })).toContainText('推理 · 低');
  await page.getByRole('button', { name: /生成图片/ }).first().click();
  await expect(page.locator('.image-mode-button')).toHaveAttribute('aria-pressed', 'true');
  await page.getByRole('button', { name: '发送' }).click();
  await expect(page.getByText('今日科技摘要')).toBeVisible();
  await expect(page.locator('.reasoning summary')).toContainText('1.2 秒');
  await expect(page.getByText('网页搜索')).toBeVisible();
  await expect(page.getByText('图像生成')).toBeVisible();
  await expect(page.locator('img.message-image')).toBeVisible();
  await expect
    .poll(() =>
      page.locator('img.message-image').evaluate((image: HTMLImageElement) => image.naturalWidth)
    )
    .toBeGreaterThan(0);
  await page.getByText('查看安全参数').click();
  await expect(page.locator('.tool-details pre')).toContainText('today technology news');
  await expect(page.getByText('Example Technology')).toBeVisible();
  await expect(page.getByText('较早上下文已摘要')).toBeVisible();
  await expect(page.getByText('安全渲染')).toBeVisible();
  await expect(page.locator('.markdown [onmouseover], .markdown script')).toHaveCount(0);
  expect(
    await page.evaluate(() => (window as typeof window & { __owuiXSS?: boolean }).__owuiXSS)
  ).toBeUndefined();
  await page.screenshot({ path: testInfo.outputPath('chat.png'), fullPage: true });
});

test('administrator can inspect another user chat without mutation controls', async ({ page }) => {
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
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    const respond = (body: unknown) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(body)
      });
    if (path === '/api/v1/me') return respond({ user: admin, csrfToken: 'admin-csrf' });
    if (path === '/api/v1/models') return respond({ models: [model] });
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
    return route.fulfill({
      status: 404,
      contentType: 'application/json',
      body: JSON.stringify({ error: { code: 'not_found', message: 'Not found.' } })
    });
  });

  await page.goto('/');
  await expect(page.getByText('管理员只读查看')).toBeVisible();
  await expect(page.getByLabel('聊天消息')).toBeDisabled();
  await expect(page.getByRole('button', { name: '推理强度' })).toBeDisabled();
  await expect(page.locator('.conversation-actions')).toHaveCount(0);
});
