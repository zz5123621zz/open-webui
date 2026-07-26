# La4RainGPT TTS backend

## Status

The backend has a provider-neutral streaming speech boundary plus two
adapters:

- Aliyun `FlowingSpeechSynthesizer`
- Volcengine V3 bidirectional streaming TTS

Speech remains disabled after migration. Enabling and provider selection are
administrator service settings and do not require an application or CPA
restart.

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
- Volcengine model 2.0 voice list:
  <https://www.volcengine.com/docs/6561/1257544?lang=zh>
- Volcengine new-console API Key management:
  <https://console.volcengine.com/speech/new/setting/apikeys?projectName=default>

The Volcengine adapter follows the current new-console V3 protocol. The
WebSocket handshake sends only `X-Api-Key`, `X-Api-Resource-Id`,
`X-Api-Connect-Id`, and the optional usage-return control header. It does not
send the legacy `X-Api-App-Key`, `X-Api-Access-Key`, or `Authorization`
headers. It uses the official binary event envelope in this order:

1. `StartConnection` / `ConnectionStarted`
2. `StartSession` / `SessionStarted`
3. zero or more `TaskRequest` events and `TTSResponse` audio events
4. `FinishSession` / `SessionFinished`
5. `FinishConnection` / `ConnectionFinished`

The default resource is `seed-tts-2.0`; output is 24 kHz, 16-bit, mono PCM.
The request enables the provider's documented Markdown and emoji filtering.

The Aliyun adapter obtains and caches temporary tokens through `CreateToken`.
Long-lived provider credentials for either adapter are never sent to the
browser.

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

## Reverse proxy requirement

The public speech endpoint must preserve the WebSocket hop-by-hop headers.
Keep this as an exact Nginx location so normal HTTP and Responses SSE requests
continue to clear the `Connection` header:

```nginx
location = /api/v1/speech/sessions {
    proxy_pass http://127.0.0.1:3001;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto https;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_buffering off;
    proxy_request_buffering off;
    proxy_cache off;
    proxy_read_timeout 900s;
    proxy_send_timeout 900s;
}
```

A missing upgrade forwarding rule reaches the Go handler as HTTP 400 before a
provider session is opened.

## Provider boundary

`internal/speech.Provider` exposes provider identity, configuration state,
voices, and `Open`. `internal/speech.Session` exposes:

- `AudioConfig`
- `SendText`
- `Finish`
- `ReadEvent`
- `Close`

Provider IDs are `aliyun` and `volcengine`. Switching providers does not
require database or public WebSocket protocol changes. A user voice retained
from the previous provider is ignored when it is not available in the newly
selected provider; the new provider's administrator-selected default is used.

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
  "provider": "volcengine",
  "defaultVoice": "zh_female_vv_uranus_bigtts"
}
```

Enabling fails with `409 speech_provider_not_configured` when the selected
adapter has no credentials. Provider and voice changes are validated and every
update is audited in SQLite. Disabling prevents new sessions; it does not tear
down sessions that were already speaking.

## User preference API

### Read preference

`GET /api/v1/me/speech`

With Volcengine selected, a new user receives:

```json
{
  "speech": {
    "mode": "manual",
    "autoRead": false,
    "speed": 1,
    "voice": "",
    "effectiveVoice": "zh_female_vv_uranus_bigtts",
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
  "voice": "zh_female_vv_uranus_bigtts"
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
{"type":"speech.connecting","provider":"volcengine"}
```

then:

```json
{
  "type": "speech.started",
  "provider": "volcengine",
  "voice": "zh_female_vv_uranus_bigtts",
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
start with speech disabled.

### Volcengine new-console V3

Only use the API Key created in the Doubao Voice new console. Do not substitute
an IAM AccessKey ID/Secret, legacy APP ID, or legacy Access Token.

The API Key and the `seed-tts-2.0` entitlement must belong to the same
Volcengine project. Projects are isolated. An upstream HTTP 403 containing
`requested resource not granted` means the key was recognized but the selected
project has not enabled Doubao Speech Synthesis 2.0 under **开通管理**.

1. In the same Volcengine project that owns the trial or paid TTS entitlement,
   create an API Key:
   <https://console.volcengine.com/speech/new/setting/apikeys?projectName=default>
2. Select a `seed-tts-2.0` voice in the voice library and copy its exact
   Speaker ID:
   <https://console.volcengine.com/speech/new/voices?projectName=default>
3. Create the deployment secret without placing the value in shell history:

   ```sh
   sudo install -m 0400 -o 65532 -g 65532 /dev/null \
     /opt/owui-personal-slim/secrets/tts_volcengine_api_key
   read -rsp 'Volcengine TTS API Key: ' VOLCENGINE_TTS_SECRET_INPUT; echo
   printf '%s' "$VOLCENGINE_TTS_SECRET_INPUT" |
     sudo tee /opt/owui-personal-slim/secrets/tts_volcengine_api_key >/dev/null
   unset VOLCENGINE_TTS_SECRET_INPUT
   sudo chown 65532:65532 \
     /opt/owui-personal-slim/secrets/tts_volcengine_api_key
   sudo chmod 0400 \
     /opt/owui-personal-slim/secrets/tts_volcengine_api_key
   ```

4. Set only non-secret values in `.env`:

   ```dotenv
   TTS_VOLCENGINE_RESOURCE_ID=seed-tts-2.0
   TTS_VOLCENGINE_VOICES=zh_female_vv_uranus_bigtts:Vivi 2.0
   ```

   Multiple allowlisted voices use comma-separated `id:label` entries. Every
   ID must be available to the same Volcengine project and must report
   `ResourceID=seed-tts-2.0`. Do not infer compatibility from the display name
   alone: a `mars` voice can be rejected by the V3 bidirectional endpoint with
   `resource ID is mismatched with speaker related resource`.

5. Recreate only the Go application once to mount the secret:

   ```sh
   docker compose \
     -f compose.yaml \
     -f compose.tts-volcengine.yaml \
     up -d app
   ```

6. Select `volcengine`, its default voice, and enable speech through
   `PUT /api/v1/admin/speech`.

The adapter always sends:

```text
X-Api-Key: <server-side secret>
X-Api-Resource-Id: seed-tts-2.0
X-Api-Connect-Id: <new UUID per provider connection>
X-Control-Require-Usage-Tokens-Return: *
```

### Aliyun

To configure Aliyun:

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

Do not place any provider key in `.env`, the database, frontend bundles,
administrator responses, or GitHub. Use a restricted RAM user rather than an
Alibaba account root AccessKey.

## Reverse proxy

The Nginx production configuration contains a dedicated WebSocket location for
`/api/v1/speech/sessions`. It preserves `Upgrade` and uses a 1900-second read
timeout. Cloudflare supports proxied WebSockets; the DNS record may remain
orange-cloud when WebSockets are enabled for the zone.
