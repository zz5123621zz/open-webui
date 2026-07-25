<script lang="ts">
  import { afterUpdate, onMount } from 'svelte';
  import { marked, Renderer } from 'marked';
  import markedKatex from 'marked-katex-extension';
  import DOMPurify from 'dompurify';
  import hljs from 'highlight.js/lib/core';
  import bash from 'highlight.js/lib/languages/bash';
  import css from 'highlight.js/lib/languages/css';
  import javascript from 'highlight.js/lib/languages/javascript';
  import json from 'highlight.js/lib/languages/json';
  import markdown from 'highlight.js/lib/languages/markdown';
  import python from 'highlight.js/lib/languages/python';
  import typescript from 'highlight.js/lib/languages/typescript';
  import { translate, type Locale } from './i18n';

  export let text = '';
  export let locale: Locale = 'zh-CN';
  let copiedButton: HTMLButtonElement | null = null;
  let markdownElement: HTMLDivElement;
  let resizeFrame = 0;

  hljs.registerLanguage('bash', bash);
  hljs.registerLanguage('shell', bash);
  hljs.registerLanguage('css', css);
  hljs.registerLanguage('javascript', javascript);
  hljs.registerLanguage('js', javascript);
  hljs.registerLanguage('json', json);
  hljs.registerLanguage('markdown', markdown);
  hljs.registerLanguage('python', python);
  hljs.registerLanguage('typescript', typescript);
  hljs.registerLanguage('ts', typescript);

  const renderer = new Renderer();
  renderer.code = ({ text: code, lang }) => {
    const language = (lang || '').split(/\s/, 1)[0].replace(/[^a-zA-Z0-9_+-]/g, '').slice(0, 24);
    const highlighted =
      language && hljs.getLanguage(language)
        ? hljs.highlight(code, { language }).value
        : hljs.highlightAuto(code).value;
    const label = language || 'code';
    return `<div class="code-shell"><div class="code-header"><span>${label}</span><button type="button" class="copy-code">${translate(locale, '复制', 'Copy')}</button></div><pre><code class="hljs language-${label}">${highlighted}</code></pre></div>`;
  };

  marked.use(
    markedKatex({ throwOnError: false, output: 'htmlAndMathml', nonStandard: true }),
    { renderer }
  );
  marked.setOptions({
    breaks: true,
    gfm: true
  });

  function renderMarkdown(value: string, _locale: Locale): string {
    const clean = DOMPurify.sanitize(marked.parse(value, { async: false }) as string, {
      USE_PROFILES: { html: true, mathMl: true }
    });
    const template = document.createElement('template');
    template.innerHTML = clean;
    for (const anchor of template.content.querySelectorAll<HTMLAnchorElement>('a[href]')) {
      anchor.rel = 'noopener noreferrer';
      try {
        const destination = new URL(anchor.href, window.location.href);
        if (destination.origin !== window.location.origin) anchor.target = '_blank';
      } catch {
        anchor.removeAttribute('href');
      }
    }
    for (const table of template.content.querySelectorAll<HTMLTableElement>('table')) {
      const shell = document.createElement('div');
      shell.className = 'rich-table-shell';
      const hint = document.createElement('span');
      hint.className = 'rich-table-hint';
      hint.setAttribute('aria-hidden', 'true');
      hint.textContent = translate(
        _locale,
        '表格可左右滑动，查看完整内容',
        'Swipe horizontally to view the full table'
      );
      const scroller = document.createElement('div');
      scroller.className = 'rich-table-scroll';
      scroller.setAttribute('role', 'region');
      scroller.setAttribute(
        'aria-label',
        translate(_locale, '可横向滚动的数据表', 'Horizontally scrollable data table')
      );
      scroller.tabIndex = 0;
      table.parentNode?.insertBefore(shell, table);
      shell.append(hint, scroller);
      scroller.append(table);
    }
    return template.innerHTML;
  }

  $: rendered = renderMarkdown(text, locale);

  function updateTableOverflow() {
    if (!markdownElement) return;
    for (const shell of markdownElement.querySelectorAll<HTMLElement>('.rich-table-shell')) {
      const scroller = shell.querySelector<HTMLElement>('.rich-table-scroll');
      shell.classList.toggle(
        'is-overflowing',
        Boolean(scroller && scroller.scrollWidth > scroller.clientWidth + 1)
      );
    }
  }

  function queueTableOverflowUpdate() {
    cancelAnimationFrame(resizeFrame);
    resizeFrame = requestAnimationFrame(updateTableOverflow);
  }

  afterUpdate(queueTableOverflowUpdate);

  onMount(() => {
    const observer =
      typeof ResizeObserver === 'undefined'
        ? null
        : new ResizeObserver(queueTableOverflowUpdate);
    if (markdownElement) observer?.observe(markdownElement);
    queueTableOverflowUpdate();
    return () => {
      cancelAnimationFrame(resizeFrame);
      observer?.disconnect();
    };
  });

  async function copyCode(event: MouseEvent) {
    const button = (event.target as HTMLElement).closest<HTMLButtonElement>('.copy-code');
    if (!button) return;
    const code = button.closest('.code-shell')?.querySelector('code')?.textContent || '';
    if (!code) return;
    await navigator.clipboard.writeText(code);
    if (copiedButton && copiedButton !== button) {
      copiedButton.textContent = translate(locale, '复制', 'Copy');
    }
    copiedButton = button;
    button.textContent = translate(locale, '已复制', 'Copied');
    setTimeout(() => {
      if (copiedButton === button) {
        button.textContent = translate(locale, '复制', 'Copy');
        copiedButton = null;
      }
    }, 1400);
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div class="markdown" bind:this={markdownElement} on:click={copyCode}>{@html rendered}</div>
