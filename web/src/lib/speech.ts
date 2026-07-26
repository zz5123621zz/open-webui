import { get, writable } from 'svelte/store';
import { getSpeechPreference, updateSpeechPreference } from './api';
import type { Message, SpeechMode, SpeechPreference } from './types';

export type SpeechPlayerStatus =
  | 'idle'
  | 'connecting'
  | 'buffering'
  | 'playing'
  | 'paused'
  | 'completed'
  | 'error';

export interface SpeechPlayerState {
  messageId: string;
  label: string;
  status: SpeechPlayerStatus;
  provider: string;
  voice: string;
  speed: number;
  volume: number;
  currentTime: number;
  duration: number;
  buffered: number;
  streaming: boolean;
  errorCode: string;
  errorMessage: string;
}

export interface SpeechDeviceAuthorization {
  acknowledged: boolean;
  active: boolean;
  needsGesture: boolean;
}

type AudioChunk = {
  start: number;
  end: number;
  samples: Float32Array;
};

type ScheduledAudio = {
  source: AudioBufferSourceNode;
  startContextTime: number;
  startSample: number;
  endSample: number;
  rate: number;
};

const emptyPlayerState: SpeechPlayerState = {
  messageId: '',
  label: '',
  status: 'idle',
  provider: '',
  voice: '',
  speed: 1,
  volume: 1,
  currentTime: 0,
  duration: 0,
  buffered: 0,
  streaming: false,
  errorCode: '',
  errorMessage: ''
};

const emptyDeviceAuthorization: SpeechDeviceAuthorization = {
  acknowledged: false,
  active: false,
  needsGesture: false
};

export const speechPreference = writable<SpeechPreference | null>(null);
export const speechPreferenceLoading = writable(false);
export const speechPreferenceError = writable('');
export const speechPlayerState = writable<SpeechPlayerState>({ ...emptyPlayerState });
export const speechDeviceAuthorization = writable<SpeechDeviceAuthorization>({
  ...emptyDeviceAuthorization
});

let currentUserId = '';

function authorizationKey(userId: string): string {
  return `personal-chat-speech-authorized-v1:${userId}`;
}

export async function initializeSpeech(userId: string): Promise<void> {
  currentUserId = userId;
  speechPreferenceLoading.set(true);
  speechPreferenceError.set('');
  speechDeviceAuthorization.set({
    acknowledged: localStorage.getItem(authorizationKey(userId)) === 'true',
    active: false,
    needsGesture: false
  });
  await refreshSpeechPreference();
}

export async function refreshSpeechPreference(): Promise<void> {
  try {
    speechPreference.set(await getSpeechPreference());
  } catch (error) {
    speechPreference.set(null);
    speechPreferenceError.set(
      error instanceof Error ? error.message : '无法读取语音设置。'
    );
  } finally {
    speechPreferenceLoading.set(false);
  }
}

export async function saveSpeechPreference(
  patch: Partial<Pick<SpeechPreference, 'mode' | 'speed' | 'voice'>>
): Promise<SpeechPreference> {
  const current = get(speechPreference);
  if (!current) throw new Error('语音设置尚未载入。');
  const next = await updateSpeechPreference(
    (patch.mode || current.mode) as SpeechMode,
    patch.speed ?? current.speed,
    patch.voice ?? current.voice
  );
  speechPreference.set(next);
  speechPreferenceError.set('');
  return next;
}

export function resetSpeech(): void {
  currentUserId = '';
  speechController.stop();
  speechPreference.set(null);
  speechPreferenceError.set('');
  speechPreferenceLoading.set(false);
  speechDeviceAuthorization.set({ ...emptyDeviceAuthorization });
}

export function messageSpeechText(message: Message): string {
  return normalizeSpeechText(
    message.parts
      .filter((part) => part.type === 'text' && part.text)
      .map((part) => part.text || '')
      .join('\n\n')
  );
}

export function normalizeSpeechText(value: string): string {
  if (!value) return '';
  let text = value.replace(/\r\n?/g, '\n');

  // Do not send an unfinished code fence because its contents can still change.
  const openFence = text.lastIndexOf('```');
  if (openFence >= 0 && (text.match(/```/g)?.length || 0) % 2 === 1) {
    text = text.slice(0, openFence);
  }
  text = text.replace(/```[\s\S]*?```/g, '\n代码内容已省略。\n');
  text = text
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\[([^\]]+)\]\((?:https?:\/\/|\/)[^)]*\)/g, '$1')
    .replace(/https?:\/\/[^\s)]+/g, '相关链接')
    .replace(/<[^>]+>/g, ' ')
    .replace(/`([^`]+)`/g, '$1')
    .replace(/^\s{0,3}#{1,6}\s+/gm, '')
    .replace(/^\s*>\s?/gm, '')
    .replace(/^\s*[-*+]\s+/gm, '')
    .replace(/^\s*\d+[.)、]\s+/gm, '')
    .replace(/^\s*\|?[\s:|-]+\|?\s*$/gm, '')
    .replace(/\|/g, '，')
    .replace(/\[(?:\d+|[a-z])\]/gi, '')
    .replace(/[*_~]/g, '')
    .replace(/[ \t]+/g, ' ')
    .replace(/\s*\n+\s*/g, '。')
    .replace(/。{2,}/g, '。')
    .replace(/\s+([，。！？；：,.!?;:])/g, '$1')
    .trim();
  return text;
}

function completeSentenceLength(value: string): number {
  let boundary = 0;
  for (const match of value.matchAll(/[。！？!?；;]/g)) {
    boundary = (match.index || 0) + match[0].length;
  }
  return boundary;
}

function splitUTF8Frames(value: string, maxBytes = 6000): string[] {
  const encoder = new TextEncoder();
  const frames: string[] = [];
  let current = '';
  let bytes = 0;
  for (const character of value) {
    const characterBytes = encoder.encode(character).byteLength;
    if (bytes + characterBytes > maxBytes && current) {
      frames.push(current.trim());
      current = '';
      bytes = 0;
    }
    current += character;
    bytes += characterBytes;
  }
  if (current.trim()) frames.push(current.trim());
  return frames;
}

class SpeechController {
  private socket: WebSocket | null = null;
  private audioContext: AudioContext | null = null;
  private gainNode: GainNode | null = null;
  private chunks: AudioChunk[] = [];
  private scheduled: ScheduledAudio[] = [];
  private sampleRate = 24000;
  private totalSamples = 0;
  private playheadSample = 0;
  private scheduleCursor = 0;
  private nextScheduleTime = 0;
  private serverSpeed = 1;
  private targetSpeed = 1;
  private localRate = 1;
  private sequence = 0;
  private queuedFrames: string[] = [];
  private started = false;
  private finishRequested = false;
  private finishSent = false;
  private providerCompleted = false;
  private sentTextLength = 0;
  private normalizedText = '';
  private currentMessage: Message | null = null;
  private generation = 0;
  private progressTimer: number | undefined;
  private autoOpeningId = '';

  async authorize(): Promise<void> {
    await this.ensureAudio(true);
  }

  async playMessage(message: Message): Promise<void> {
    const state = get(speechPlayerState);
    if (state.messageId === message.id) {
      if (state.status === 'playing' || state.status === 'buffering') {
        await this.pause();
        return;
      }
      if (state.status === 'paused') {
        await this.resume();
        return;
      }
      if (state.status === 'completed' && this.totalSamples > 0) {
        await this.replay();
        return;
      }
    }
    const text = messageSpeechText(message);
    if (!text) return;
    await this.openMessage(message, false);
    this.ingestText(text, message.status !== 'streaming');
  }

  syncAutomatic(message: Message): void {
    const preference = get(speechPreference);
    const authorization = get(speechDeviceAuthorization);
    if (
      !preference ||
      preference.mode !== 'auto' ||
      !preference.serviceEnabled ||
      !preference.providerConfigured
    ) return;

    const normalized = messageSpeechText(message);
    const final = message.status !== 'streaming' && message.status !== 'pending';
    if (!normalized) return;
    if (
      this.currentMessage?.id === message.id &&
      get(speechPlayerState).status !== 'error'
    ) {
      this.ingestText(normalized, final);
      return;
    }
    if (!final && completeSentenceLength(normalized) === 0) return;
    if (!authorization.acknowledged) {
      speechDeviceAuthorization.update((value) => ({ ...value, needsGesture: true }));
      return;
    }
    if (this.autoOpeningId === message.id) return;
    this.autoOpeningId = message.id;
    void this.openMessage(message, true)
      .then(() => this.ingestText(normalized, final))
      .catch(() => {
        // openMessage already published a localized player state.
      })
      .finally(() => {
        if (this.autoOpeningId === message.id) this.autoOpeningId = '';
      });
  }

  syncMessage(message: Message): void {
    if (this.currentMessage?.id !== message.id) return;
    const state = get(speechPlayerState);
    if (state.status === 'error' || state.status === 'idle') return;
    const normalized = messageSpeechText(message);
    if (!normalized) return;
    const final = message.status !== 'streaming' && message.status !== 'pending';
    this.ingestText(normalized, final);
  }

  async pause(): Promise<void> {
    const state = get(speechPlayerState);
    if (!this.audioContext || !['playing', 'buffering'].includes(state.status)) return;
    this.updateProgress();
    await this.audioContext.suspend();
    this.patchState({ status: 'paused' });
    this.updateMediaSession('paused');
  }

  async resume(): Promise<void> {
    const state = get(speechPlayerState);
    if (!this.audioContext || state.status !== 'paused') return;
    await this.audioContext.resume();
    if (this.audioContext.state !== 'running') {
      this.fail('speech_authorization_required', '浏览器需要你再次点击以允许播放声音。');
      return;
    }
    this.patchState({ status: this.totalSamples > this.playheadSample ? 'playing' : 'buffering' });
    this.scheduleAvailable();
    this.updateMediaSession('playing');
  }

  async replay(): Promise<void> {
    if (!this.audioContext || this.totalSamples === 0) return;
    await this.audioContext.resume();
    this.providerCompleted = true;
    this.restartPlayback(0);
    this.patchState({ status: 'playing', currentTime: 0 });
    this.updateMediaSession('playing');
  }

  seek(seconds: number): void {
    if (!this.audioContext || this.totalSamples === 0) return;
    const sample = Math.round(
      Math.max(0, Math.min(seconds, this.totalSamples / this.sampleRate)) * this.sampleRate
    );
    this.restartPlayback(sample);
    this.patchState({ currentTime: sample / this.sampleRate });
  }

  skip(seconds: number): void {
    const state = get(speechPlayerState);
    this.seek(state.currentTime + seconds);
  }

  setVolume(volume: number): void {
    const value = Math.max(0, Math.min(volume, 1));
    if (this.gainNode) this.gainNode.gain.value = value;
    this.patchState({ volume: value });
  }

  setSpeed(speed: number): void {
    const value = Math.max(0.5, Math.min(speed, 2));
    this.updateProgress();
    this.targetSpeed = value;
    this.localRate = Math.max(0.25, Math.min(value / this.serverSpeed, 4));
    if (this.totalSamples > 0) this.restartPlayback(this.playheadSample);
    this.patchState({ speed: value });
  }

  stop(): void {
    this.generation += 1;
    this.autoOpeningId = '';
    this.closeSocket(true);
    this.stopSources();
    this.chunks = [];
    this.totalSamples = 0;
    this.playheadSample = 0;
    this.scheduleCursor = 0;
    this.currentMessage = null;
    this.normalizedText = '';
    this.sentTextLength = 0;
    this.providerCompleted = false;
    this.finishRequested = false;
    this.finishSent = false;
    this.started = false;
    speechPlayerState.set({ ...emptyPlayerState });
    this.updateMediaSession('none');
    if (this.progressTimer !== undefined) {
      window.clearInterval(this.progressTimer);
      this.progressTimer = undefined;
    }
  }

  private async openMessage(message: Message, automatic: boolean): Promise<void> {
    const text = messageSpeechText(message);
    if (!text) throw new Error('没有可朗读的正文。');
    const generation = ++this.generation;
    this.closeSocket(true);
    this.stopSources();
    this.resetBuffers();
    this.currentMessage = message;
    this.normalizedText = '';
    this.sentTextLength = 0;

    try {
      await this.ensureAudio(!automatic);
    } catch (error) {
      this.fail(
        'speech_authorization_required',
        automatic
          ? '自动朗读需要先在“语音与朗读”中完成本设备授权。'
          : error instanceof Error
            ? error.message
            : '浏览器未允许播放声音。'
      );
      throw error;
    }
    if (generation !== this.generation) return;

    const preference = get(speechPreference);
    this.targetSpeed = preference?.speed || 1;
    const label = text.length > 34 ? `${text.slice(0, 34)}…` : text;
    speechPlayerState.set({
      ...emptyPlayerState,
      messageId: message.id,
      label,
      status: 'connecting',
      speed: this.targetSpeed,
      volume: this.gainNode?.gain.value ?? 1,
      streaming: message.status === 'streaming'
    });
    this.ensureTimer();
    this.configureMediaSession(label);

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const socket = new WebSocket(
      `${protocol}//${window.location.host}/api/v1/speech/sessions`
    );
    this.socket = socket;
    socket.binaryType = 'arraybuffer';
    socket.onmessage = (event) => {
      if (generation !== this.generation) return;
      if (typeof event.data === 'string') {
        this.handleServerMessage(event.data);
      } else if (event.data instanceof ArrayBuffer) {
        this.appendPCM(event.data);
      } else if (event.data instanceof Blob) {
        void event.data.arrayBuffer().then((buffer) => {
          if (generation === this.generation) this.appendPCM(buffer);
        });
      }
    };
    socket.onerror = () => {
      if (generation === this.generation) {
        this.fail('speech_network_error', '语音连接出现异常，请稍后重试。');
      }
    };
    socket.onclose = () => {
      if (generation !== this.generation) return;
      const state = get(speechPlayerState);
      if (
        !this.providerCompleted &&
        !['idle', 'error', 'completed'].includes(state.status)
      ) {
        this.fail('speech_connection_closed', '语音连接提前结束，请重试。');
      }
    };
  }

  private ingestText(normalized: string, final: boolean): void {
    if (!this.currentMessage || !normalized) return;
    this.normalizedText = normalized;
    if (this.sentTextLength > normalized.length) return;
    const available = normalized.slice(this.sentTextLength);
    const sendLength = final ? available.length : completeSentenceLength(available);
    if (sendLength > 0) {
      const ready = available.slice(0, sendLength);
      this.queuedFrames.push(...splitUTF8Frames(ready));
      this.sentTextLength += sendLength;
      this.flushFrames();
    }
    if (final && !this.finishRequested && this.sentTextLength >= normalized.length) {
      this.finishRequested = true;
      this.flushFrames();
    }
    this.patchState({ streaming: !final });
  }

  private flushFrames(): void {
    if (!this.started || !this.socket || this.socket.readyState !== WebSocket.OPEN) return;
    while (this.queuedFrames.length) {
      const text = this.queuedFrames.shift();
      if (!text) continue;
      this.sequence += 1;
      this.socket.send(JSON.stringify({
        type: 'speech.text',
        sequence: this.sequence,
        text
      }));
    }
    if (this.finishRequested && !this.finishSent) {
      this.socket.send(JSON.stringify({ type: 'speech.finish' }));
      this.finishSent = true;
    }
  }

  private handleServerMessage(value: string): void {
    let message: Record<string, any>;
    try {
      message = JSON.parse(value) as Record<string, any>;
    } catch {
      this.fail('speech_invalid_event', '语音服务返回了无法解析的事件。');
      return;
    }
    const type = String(message.type || '');
    if (type === 'speech.connecting') {
      this.patchState({ provider: String(message.provider || '') });
      return;
    }
    if (type === 'speech.started') {
      const audio = message.audio as Record<string, unknown> | undefined;
      const sampleRate = Number(audio?.sampleRate || 0);
      const channels = Number(audio?.channels || 0);
      const bitDepth = Number(audio?.bitDepth || 0);
      const format = String(audio?.format || '').toLowerCase();
      if (
        sampleRate < 8000 ||
        channels !== 1 ||
        bitDepth !== 16 ||
        (format !== 'pcm' && format !== 's16le')
      ) {
        this.fail('speech_audio_unsupported', '浏览器暂不支持服务端返回的音频格式。');
        return;
      }
      this.sampleRate = sampleRate;
      this.serverSpeed = Number(message.speed || 1) || 1;
      this.localRate = Math.max(0.25, Math.min(this.targetSpeed / this.serverSpeed, 4));
      this.started = true;
      this.patchState({
        status: 'buffering',
        provider: String(message.provider || ''),
        voice: String(message.voice || ''),
        speed: this.targetSpeed
      });
      this.flushFrames();
      return;
    }
    if (type === 'speech.completed') {
      this.providerCompleted = true;
      this.patchState({ streaming: false });
      if (this.totalSamples === 0) {
        this.patchState({ status: 'completed' });
      }
      return;
    }
    if (type === 'speech.cancelled') {
      this.stop();
      return;
    }
    if (type === 'speech.error') {
      const code = String(message.code || 'speech_failed');
      this.fail(code, this.localizeServerError(code));
    }
  }

  private localizeServerError(code: string): string {
    const errors: Record<string, string> = {
      speech_session_limit: '当前朗读任务较多，请稍后重试。',
      speech_disabled: '管理员暂时关闭了文字转语音服务。',
      speech_provider_unavailable: '语音提供商尚未正确配置。',
      speech_provider_not_granted:
        '火山引擎项目尚未开通“豆包语音合成模型 2.0”，请管理员在开通管理中启用 seed-tts-2.0。',
      speech_provider_auth_failed:
        '火山引擎拒绝了当前 API Key，请管理员检查密钥及所属项目。',
      speech_voice_unavailable: '所选音色已经不可用，请在设置中重新选择。',
      speech_voice_model_mismatch:
        '当前音色与“豆包语音合成模型 2.0”不兼容，请管理员更换为 TTS 2.0 音色。',
      speech_session_expired: '本次朗读时间过长，语音会话已结束。',
      speech_text_limit: '这条回答过长，已达到单次朗读上限。',
      speech_provider_failed: '火山引擎未能完成本次语音合成，请重试。'
    };
    return errors[code] || '语音合成失败，请稍后重试。';
  }

  private appendPCM(buffer: ArrayBuffer): void {
    if (buffer.byteLength < 2) return;
    const view = new DataView(buffer);
    const length = Math.floor(buffer.byteLength / 2);
    const samples = new Float32Array(length);
    for (let index = 0; index < length; index += 1) {
      const value = view.getInt16(index * 2, true);
      samples[index] = value < 0 ? value / 32768 : value / 32767;
    }
    const start = this.totalSamples;
    const end = start + samples.length;
    this.chunks.push({ start, end, samples });
    this.totalSamples = end;
    const duration = end / this.sampleRate;
    this.patchState({ duration, buffered: duration });
    if (get(speechPlayerState).status !== 'paused') {
      this.scheduleAvailable();
      this.patchState({ status: 'playing' });
      this.updateMediaSession('playing');
    }
  }

  private scheduleAvailable(): void {
    if (!this.audioContext || !this.gainNode) return;
    const state = get(speechPlayerState);
    if (['paused', 'idle', 'error', 'completed'].includes(state.status)) return;
    const now = this.audioContext.currentTime;
    if (this.nextScheduleTime < now) this.nextScheduleTime = now + 0.035;
    const scheduleUntil = now + 1.6;
    while (
      this.scheduleCursor < this.totalSamples &&
      this.nextScheduleTime < scheduleUntil
    ) {
      const chunk = this.chunks.find(
        (candidate) =>
          candidate.start <= this.scheduleCursor && candidate.end > this.scheduleCursor
      );
      if (!chunk) break;
      const offset = this.scheduleCursor - chunk.start;
      const sourceSamples = chunk.samples.subarray(offset);
      const audioBuffer = this.audioContext.createBuffer(
        1,
        sourceSamples.length,
        this.sampleRate
      );
      audioBuffer.copyToChannel(sourceSamples, 0);
      const source = this.audioContext.createBufferSource();
      source.buffer = audioBuffer;
      source.playbackRate.value = this.localRate;
      source.connect(this.gainNode);
      const startContextTime = this.nextScheduleTime;
      const startSample = this.scheduleCursor;
      const endSample = startSample + sourceSamples.length;
      source.start(startContextTime);
      this.scheduled.push({
        source,
        startContextTime,
        startSample,
        endSample,
        rate: this.localRate
      });
      this.scheduleCursor = endSample;
      this.nextScheduleTime =
        startContextTime + sourceSamples.length / this.sampleRate / this.localRate;
    }
  }

  private restartPlayback(sample: number): void {
    if (!this.audioContext) return;
    const status = get(speechPlayerState).status;
    this.stopSources();
    this.playheadSample = Math.max(0, Math.min(sample, this.totalSamples));
    this.scheduleCursor = this.playheadSample;
    this.nextScheduleTime = this.audioContext.currentTime + 0.035;
    if (status !== 'paused') {
      this.patchState({
        status:
          this.playheadSample < this.totalSamples
            ? 'playing'
            : this.providerCompleted
              ? 'completed'
              : 'buffering'
      });
      this.scheduleAvailable();
    }
  }

  private updateProgress(): void {
    if (!this.audioContext || this.totalSamples === 0) return;
    const now = this.audioContext.currentTime;
    let sample = this.playheadSample;
    for (const item of this.scheduled) {
      if (now < item.startContextTime) break;
      sample = Math.min(
        item.endSample,
        item.startSample +
          Math.max(0, now - item.startContextTime) * this.sampleRate * item.rate
      );
    }
    this.playheadSample = Math.max(0, Math.min(sample, this.totalSamples));
    this.scheduled = this.scheduled.filter((item) => {
      const duration =
        (item.endSample - item.startSample) / this.sampleRate / item.rate;
      if (now <= item.startContextTime + duration + 0.25) return true;
      item.source.disconnect();
      return false;
    });
    const state = get(speechPlayerState);
    if (
      this.providerCompleted &&
      this.playheadSample >= this.totalSamples - 32 &&
      state.status !== 'paused'
    ) {
      this.playheadSample = this.totalSamples;
      this.stopSources();
      this.patchState({
        status: 'completed',
        currentTime: this.totalSamples / this.sampleRate,
        streaming: false
      });
      this.updateMediaSession('paused');
      return;
    }
    if (state.status !== 'paused') this.scheduleAvailable();
    this.patchState({ currentTime: this.playheadSample / this.sampleRate });
  }

  private async ensureAudio(explicit: boolean): Promise<void> {
    const AudioContextConstructor =
      window.AudioContext ||
      (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
    if (!AudioContextConstructor) {
      throw new Error('当前浏览器不支持实时语音播放。');
    }
    if (!this.audioContext || this.audioContext.state === 'closed') {
      this.audioContext = new AudioContextConstructor();
      this.gainNode = this.audioContext.createGain();
      this.gainNode.gain.value = get(speechPlayerState).volume || 1;
      this.gainNode.connect(this.audioContext.destination);
    }
    await this.audioContext.resume();
    if (this.audioContext.state !== 'running') {
      speechDeviceAuthorization.update((value) => ({
        ...value,
        active: false,
        needsGesture: true
      }));
      throw new Error('请点击“允许播放声音”完成浏览器授权。');
    }
    const silent = this.audioContext.createBuffer(1, 1, this.audioContext.sampleRate);
    const source = this.audioContext.createBufferSource();
    source.buffer = silent;
    source.connect(this.gainNode!);
    source.start();
    if (explicit && currentUserId) {
      localStorage.setItem(authorizationKey(currentUserId), 'true');
    }
    speechDeviceAuthorization.set({
      acknowledged: explicit || get(speechDeviceAuthorization).acknowledged,
      active: true,
      needsGesture: false
    });
  }

  private resetBuffers(): void {
    this.chunks = [];
    this.scheduled = [];
    this.sampleRate = 24000;
    this.totalSamples = 0;
    this.playheadSample = 0;
    this.scheduleCursor = 0;
    this.nextScheduleTime = 0;
    this.serverSpeed = 1;
    this.localRate = 1;
    this.sequence = 0;
    this.queuedFrames = [];
    this.started = false;
    this.finishRequested = false;
    this.finishSent = false;
    this.providerCompleted = false;
  }

  private closeSocket(cancel: boolean): void {
    if (!this.socket) return;
    if (cancel && this.socket.readyState === WebSocket.OPEN) {
      this.socket.send(JSON.stringify({ type: 'speech.cancel' }));
    }
    this.socket.onclose = null;
    this.socket.onerror = null;
    this.socket.onmessage = null;
    this.socket.close(1000);
    this.socket = null;
  }

  private stopSources(): void {
    for (const item of this.scheduled) {
      try {
        item.source.stop();
      } catch {
        // A source that has already ended is safe to ignore.
      }
      item.source.disconnect();
    }
    this.scheduled = [];
  }

  private fail(code: string, message: string): void {
    this.closeSocket(false);
    this.stopSources();
    this.patchState({ status: 'error', errorCode: code, errorMessage: message });
    this.updateMediaSession('paused');
  }

  private patchState(patch: Partial<SpeechPlayerState>): void {
    speechPlayerState.update((value) => ({ ...value, ...patch }));
  }

  private ensureTimer(): void {
    if (this.progressTimer !== undefined) return;
    this.progressTimer = window.setInterval(() => this.updateProgress(), 100);
  }

  private configureMediaSession(label: string): void {
    if (!('mediaSession' in navigator)) return;
    try {
      navigator.mediaSession.metadata = new MediaMetadata({
        title: label,
        artist: 'La4RainGPT',
        album: '回答朗读'
      });
      navigator.mediaSession.setActionHandler('play', () => void this.resume());
      navigator.mediaSession.setActionHandler('pause', () => void this.pause());
      navigator.mediaSession.setActionHandler('seekbackward', (details) =>
        this.skip(-(details.seekOffset || 10))
      );
      navigator.mediaSession.setActionHandler('seekforward', (details) =>
        this.skip(details.seekOffset || 10)
      );
      navigator.mediaSession.setActionHandler('seekto', (details) => {
        if (details.seekTime !== undefined) this.seek(details.seekTime);
      });
      navigator.mediaSession.setActionHandler('stop', () => this.stop());
    } catch {
      // Media Session is an enhancement and must not block playback.
    }
  }

  private updateMediaSession(state: MediaSessionPlaybackState | 'none'): void {
    if (!('mediaSession' in navigator)) return;
    try {
      navigator.mediaSession.playbackState = state;
      if (state === 'none') {
        navigator.mediaSession.metadata = null;
        return;
      }
      const player = get(speechPlayerState);
      if (player.duration > 0 && player.currentTime <= player.duration) {
        navigator.mediaSession.setPositionState({
          duration: player.duration,
          playbackRate: this.localRate,
          position: Math.min(player.currentTime, player.duration)
        });
      }
    } catch {
      // Some mobile browsers expose a partial Media Session implementation.
    }
  }
}

export const speechController = new SpeechController();
