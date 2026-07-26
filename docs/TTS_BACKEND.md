# La4RainGPT TTS backend

## Status

The backend has a provider-neutral streaming speech boundary and an Aliyun
`FlowingSpeechSynthesizer` adapter. Speech remains disabled after migration.
The Volcengine adapter is intentionally not implemented yet; it can implement
the same `speech.Provider` and `speech.Session` interfaces without changing the
browser protocol, stored preferences, or administrator API.

The administrator and user frontend controls are a separate UI task. This
document describes the backend contract they consume.

## Official provider documentation

- Aliyun streaming text WebSocket protocol:
  <https://help.aliyun.com/zh/isi/developer-reference/websocket-protocol-description>
- Aliyun flowing speech service description:
  <https://help.aliyun.com/zh/isi/developer-reference/interface-description/>
- Aliyun CreateToken OpenAPI and POP signature:
  <https://help.aliyun.com/zh/isi/getting-started/use-http-or-https-to-obtain-an-access-token>
- Aliyun token overview:
  <https://help.aliyun.com/zh/isi/getting-started/obtain-an-access-token/>
- Volcengine current TTS API list and historical V1 protocol:
  <https://www.volcengine.com/docs/6561/2228192?lang=zh>
- Volcengine V3 bidirectional streaming TTS:
  <https://www.volcengine.com/docs/6561/2532486?lang=zh>
- Volcengine authentication:
  <https://www.volcengine.com/docs/6561/1105162?lang=zh>

Aliyun was selected first because its flowing protocol accepts incremental
`RunSynthesis` text and emits one continuous audio stream. The adapter obtains
and caches temporary tokens through `CreateToken`; long-lived AccessKeys are
never sent to the browser.

## Runtime model

- Speech concurrency is independent from CPA response concurrency.
- At most one active speech session is allowed per user.
- At most two active speech sessions are allowed for the whole application.
- Limits are immediate rejection, not a queue. A rejected connection receives
  HTTP `429 speech_session_limit`.
- A speech session lasts at most 30 minutes by default.
- Settings are read at session start. Administrator changes affect new
  sessions immediately and do not restart CPA or the Go application.
- A session belongs to its listener. Closing the tab or WebSocket ends speech;
  audio is not retained or resumed.
- Only assistant answer text may be sent. Reasoning summaries, hidden reasoning,
  tool arguments, tool results, URLs, and progress-card text must not be sent by
  the frontend.

The audio stream is 24 kHz, 16-bit, mono PCM. Every binary WebSocket frame is a
piece of one continuous PCM stream. The browser must append frames in order and
must not treat each frame as a standalone audio file.

## Provider boundary

`internal/speech.Provider` exposes provider identity, configuration state,
voices, and `Open`. `internal/speech.Session` exposes:

- `AudioConfig`
- `SendText`
- `Finish`
- `ReadEvent`
- `Close`

Adding Volcengine requires a new adapter plus registry/config wiring. It does
not require changes to the database or public WebSocket protocol.

## Administrator API

All requests require an authenticated administrator session.

### Read settings

`GET /api/v1/admin/speech`

The response includes `enabled`, `provider`, `defaultVoice`, provider
configuration state, allowed voices, and the fixed concurrency limits.
Credentials are never returned.

### Update settings

`PUT /api/v1/admin/speech`

The request requires the normal CSRF header:

```json
{
  "enabled": true,
  "provider": "aliyun",
  "defaultVoice": "longxiaochun"
}
```

Enabling fails with `409 speech_provider_not_configured` when the selected
adapter has no credentials. Provider and voice changes are validated and every
update is audited in SQLite. Disabling prevents new sessions; it does not tear
down sessions that were already speaking.

## User preference API

### Read preference

`GET /api/v1/me/speech`

New users receive:

```json
{
  "speech": {
    "mode": "manual",
    "autoRead": false,
    "speed": 1,
    "voice": "",
    "effectiveVoice": "longxiaochun",
    "audioAuthorization": "required_on_each_device"
  }
}
```

An empty `voice` inherits the administrator's current default.

### Update preference

`PUT /api/v1/me/speech`

```json
{
  "mode": "auto",
  "speed": 1.1,
  "voice": "longxiaochun"
}
```

- `mode` is `manual` or `auto`.
- `speed` is from `0.5` to `2.0`.
- `voice` is empty or one of the provider's allowlisted voices.

The browser's audio authorization is device-specific and cannot be granted by
the server. The future frontend must perform a user-gesture playback test the
first time auto-read is enabled on each device, then remember that local
authorization on that device. Storing `mode=auto` does not bypass browser
autoplay rules.

## Streaming session protocol

Open an authenticated, same-origin WebSocket:

`wss://chat.la4rain.com/api/v1/speech/sessions`

The server sends:

```json
{"type":"speech.connecting","provider":"aliyun"}
```

then:

```json
{
  "type": "speech.started",
  "provider": "aliyun",
  "voice": "longxiaochun",
  "speed": 1,
  "audio": {
    "format": "pcm",
    "sampleRate": 24000,
    "channels": 1,
    "bitDepth": 16
  }
}
```

Send spoken answer text in order. Sequence starts at 1 and has no gaps:

```json
{"type":"speech.text","sequence":1,"text":"这是第一句。"}
{"type":"speech.text","sequence":2,"text":"这是第二句。"}
{"type":"speech.finish"}
```

The server forwards audio as binary frames. It ends with:

```json
{"type":"speech.completed","textBytes":45}
```

The client can send `speech.cancel` or `speech.ping`. A text frame is limited to
8 KiB and a session to 200 KiB. The frontend should segment completed sentences
instead of sending every model token; this matches provider cadence and avoids
speaking half-formed Markdown.

## Docker configuration

The normal `compose.yaml` does not require TTS credentials and continues to
start with speech disabled. To configure Aliyun:

1. Put the project AppKey in `.env` as `TTS_ALIYUN_APP_KEY`.
2. Create `secrets/tts_aliyun_access_key_id` and
   `secrets/tts_aliyun_access_key_secret`, mode `0600`, with no trailing
   commentary.
3. Optionally set `TTS_ALIYUN_VOICES` as comma-separated `id:label` pairs.
4. Recreate only the Go app once to mount credentials:

   ```sh
   docker compose -f compose.yaml -f compose.tts.yaml up -d app
   ```

5. Enable speech through `PUT /api/v1/admin/speech`.

The one-time app recreation is needed only to add or rotate provider
credentials. Routine enable/disable, provider selection, and default voice
changes are runtime operations. CPA is never restarted.

Do not place AccessKey values in `.env`, the database, frontend bundles, or
administrator responses. Use a restricted RAM user rather than an Alibaba
account root AccessKey.

## Reverse proxy

The Nginx production configuration contains a dedicated WebSocket location for
`/api/v1/speech/sessions`. It preserves `Upgrade` and uses a 1900-second read
timeout. Cloudflare supports proxied WebSockets; the DNS record may remain
orange-cloud when WebSockets are enabled for the zone.
