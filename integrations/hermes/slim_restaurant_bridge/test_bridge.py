from __future__ import annotations

import asyncio
import io
import json
import stat
import sys
import tempfile
import types
import unittest
import urllib.error
from pathlib import Path
from types import SimpleNamespace
from unittest import mock

from integrations.hermes.slim_restaurant_bridge import bridge


TOKEN = "hbr_" + ("A" * 43)


def make_settings(directory: Path, *, token: str = TOKEN) -> bridge.BridgeSettings:
    return bridge.BridgeSettings(
        base_url="http://slim.test:3000",
        token=token,
        timeout_seconds=30,
        max_audio_bytes=1024 * 1024,
        max_total_audio_bytes=4 * 1024 * 1024,
        media_dir=directory,
        profile_identity="profile-a",
    )


def make_source(
    *,
    platform: str = "weixin",
    chat_type: str = "dm",
    chat_id: str = "wx-chat-1",
) -> SimpleNamespace:
    return SimpleNamespace(
        platform=SimpleNamespace(value=platform),
        chat_type=chat_type,
        chat_id=chat_id,
        user_id="wx-user-1",
    )


def make_event(
    *,
    source: SimpleNamespace | None = None,
    text: str = "为我设计20道菜",
    message_id: str = "message-1",
    message_type: str = "text",
    internal: bool = False,
    command: str | None = None,
) -> SimpleNamespace:
    return SimpleNamespace(
        source=source or make_source(),
        text=text,
        message_id=message_id,
        message_type=SimpleNamespace(value=message_type),
        internal=internal,
        get_command=lambda: command,
    )


def make_audio_file(audio_id: str, byte_size: int = 46) -> bridge.AudioFile:
    return bridge.AudioFile(
        id=audio_id,
        file_name=f"{audio_id}.wav",
        byte_size=byte_size,
        download_path=bridge.AUDIO_PATH_PREFIX + audio_id,
    )


def make_turn_result(
    *,
    request_id: str = "request-1",
    kind: str = "answer",
    text: str = "完整文字答案",
    audio_status: str = "ready",
    files: tuple[bridge.AudioFile, ...] = (),
    audio_code: str = "",
) -> bridge.TurnResult:
    return bridge.TurnResult(
        request_id=request_id,
        kind=kind,
        text=text,
        audio=bridge.AudioResult(
            status=audio_status,
            code=audio_code,
            files=files,
        ),
    )


def turn_json(
    request_id: str,
    *,
    kind: str = "clarification",
    text: str = "三道澄清问题",
    audio_status: str = "not_applicable",
    files: list[dict[str, object]] | None = None,
) -> bytes:
    return json.dumps(
        {
            "requestId": request_id,
            "kind": kind,
            "text": text,
            "audio": {
                "status": audio_status,
                "code": "",
                "files": files or [],
            },
        },
        ensure_ascii=False,
    ).encode()


def minimal_wav(payload: bytes = b"\x00\x00") -> bytes:
    body = b"WAVE" + (b"\x00" * 32) + payload
    return b"RIFF" + len(body).to_bytes(4, "little") + body


class FakeResponse:
    def __init__(
        self,
        body: bytes,
        *,
        content_type: str,
        content_length: int | None = None,
        status: int = 200,
    ) -> None:
        self.status = status
        self.headers: dict[str, str] = {"Content-Type": content_type}
        if content_length is not None:
            self.headers["Content-Length"] = str(content_length)
        self._stream = io.BytesIO(body)

    def __enter__(self) -> FakeResponse:
        return self

    def __exit__(self, *args: object) -> None:
        return None

    def read(self, size: int = -1) -> bytes:
        return self._stream.read(size)


class FakeOpener:
    def __init__(self, responses: list[object]) -> None:
        self.responses = list(responses)
        self.requests: list[object] = []

    def open(self, request: object, timeout: float) -> object:
        del timeout
        self.requests.append(request)
        response = self.responses.pop(0)
        if isinstance(response, BaseException):
            raise response
        return response


class FakeAdapter:
    def __init__(self) -> None:
        self.events: list[tuple[object, ...]] = []
        self.voice_paths: list[Path] = []
        self.fail_voice_at: int | None = None
        self.fail_typing = False

    async def send(self, chat_id: str, content: str) -> SimpleNamespace:
        self.events.append(("text", chat_id, content))
        return SimpleNamespace(success=True)

    async def send_voice(
        self,
        *,
        chat_id: str,
        audio_path: str,
        caption: str,
    ) -> SimpleNamespace:
        path = Path(audio_path)
        self.voice_paths.append(path)
        self.events.append(("voice", chat_id, path.name, caption))
        if self.fail_voice_at == len(self.voice_paths):
            return SimpleNamespace(success=False)
        return SimpleNamespace(success=True)

    async def send_typing(self, chat_id: str) -> None:
        self.events.append(("typing", chat_id))
        if self.fail_typing:
            raise RuntimeError("typing unavailable")

    async def stop_typing(self, chat_id: str) -> None:
        self.events.append(("stop_typing", chat_id))


class FakeAsyncSessionStore:
    def __init__(self, session_id: str = "hermes-session-1") -> None:
        self.session_id = session_id
        self.sources: list[object] = []

    async def get_or_create_session(self, source: object) -> SimpleNamespace:
        self.sources.append(source)
        return SimpleNamespace(session_id=self.session_id)


class FakeGateway:
    def __init__(self, adapter: FakeAdapter) -> None:
        self.adapter = adapter
        self.async_session_store = FakeAsyncSessionStore()

    def _adapter_for_source(self, source: object) -> FakeAdapter:
        del source
        return self.adapter


class BridgePureTests(unittest.TestCase):
    def test_event_eligibility_is_fail_closed(self) -> None:
        eligible = make_event()
        self.assertTrue(bridge._eligible_event(eligible, eligible.source))

        cases = [
            make_event(source=make_source(platform="telegram")),
            make_event(source=make_source(chat_type="group")),
            make_event(text=" "),
            make_event(text="/help"),
            make_event(message_type="image"),
            make_event(internal=True),
            make_event(command="help"),
        ]
        for event in cases:
            with self.subTest(event=event):
                self.assertFalse(bridge._eligible_event(event, event.source))
        self.assertFalse(bridge._eligible_event(eligible, None))

    def test_profile_scoped_settings_and_authorization(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            profile_home = Path(temporary) / "father-restaurant"

            class FakeSecretScope:
                def __init__(self) -> None:
                    self.current: dict[str, str] | None = None
                    self.reset_calls: list[object] = []

                def build_profile_secret_scope(self, home: Path) -> dict[str, str]:
                    self.asserted_home = home
                    return {
                        "SLIM_RESTAURANT_BRIDGE_TOKEN": TOKEN,
                        "SLIM_RESTAURANT_BRIDGE_URL": "HTTP://slim.test:3000/",
                        "SLIM_RESTAURANT_BRIDGE_TIMEOUT_SECONDS": "45",
                        "SLIM_RESTAURANT_BRIDGE_MAX_AUDIO_BYTES": "4096",
                        "SLIM_RESTAURANT_BRIDGE_MAX_TOTAL_AUDIO_BYTES": "8192",
                    }

                def set_secret_scope(self, values: dict[str, str]) -> object:
                    previous = self.current
                    self.current = values
                    return previous

                def reset_secret_scope(self, token: object) -> None:
                    self.reset_calls.append(token)
                    self.current = token  # type: ignore[assignment]

                def get_secret(self, name: str, default: str) -> str:
                    if self.current is None:
                        return default
                    return self.current.get(name, default)

            secret_scope = FakeSecretScope()
            agent_module = types.ModuleType("agent")
            agent_module.secret_scope = secret_scope  # type: ignore[attr-defined]
            gateway = SimpleNamespace(
                _resolve_profile_home_for_source=lambda source: profile_home,
                _is_user_authorized=lambda source: True,
            )
            with mock.patch.dict(sys.modules, {"agent": agent_module}):
                resolved = bridge._resolve_settings_and_authorization(
                    gateway,
                    make_source(),
                )

            self.assertIsNotNone(resolved)
            settings, authorized = resolved or (None, False)
            self.assertTrue(authorized)
            self.assertEqual(settings.base_url, "http://slim.test:3000")
            self.assertEqual(settings.token, TOKEN)
            self.assertEqual(settings.timeout_seconds, 45)
            self.assertEqual(settings.max_audio_bytes, 4096)
            self.assertEqual(settings.max_total_audio_bytes, 8192)
            self.assertEqual(
                settings.media_dir,
                profile_home / "media" / "slim-restaurant-bridge",
            )
            self.assertEqual(secret_scope.asserted_home, profile_home)
            self.assertEqual(secret_scope.reset_calls, [None])
            self.assertIsNone(secret_scope.current)

    def test_missing_or_invalid_profile_configuration_is_not_accepted(self) -> None:
        class FakeSecretScope:
            def build_profile_secret_scope(self, home: Path) -> dict[str, str]:
                del home
                return {}

            def set_secret_scope(self, values: dict[str, str]) -> None:
                self.values = values
                return None

            def reset_secret_scope(self, token: object) -> None:
                del token

            def get_secret(self, name: str, default: str) -> str:
                del name
                return default

        agent_module = types.ModuleType("agent")
        agent_module.secret_scope = FakeSecretScope()  # type: ignore[attr-defined]
        gateway = SimpleNamespace(
            _resolve_profile_home_for_source=lambda source: Path("/profile"),
            _is_user_authorized=lambda source: True,
        )
        with mock.patch.dict(sys.modules, {"agent": agent_module}):
            self.assertIsNone(
                bridge._resolve_settings_and_authorization(
                    gateway,
                    make_source(),
                )
            )

    def test_request_id_is_stable_and_profile_scoped(self) -> None:
        event = make_event(message_id="same-message")
        source = event.source
        first = bridge._request_id(event, source, "profile-a")
        second = bridge._request_id(event, source, "profile-a")
        other_profile = bridge._request_id(event, source, "profile-b")
        self.assertEqual(first, second)
        self.assertNotEqual(first, other_profile)
        self.assertRegex(first, r"^wx_[0-9a-f]{64}$")

        without_message_id = bridge._request_id(
            make_event(message_id=""),
            source,
            "profile-a",
        )
        self.assertRegex(without_message_id, r"^wx_[0-9a-f]{32}$")

    def test_turn_result_validation_enforces_audio_contract(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            settings = make_settings(Path(temporary))
            audio = {
                "id": "audio-1",
                "fileName": "answer.wav",
                "contentType": "audio/wav",
                "byteSize": len(minimal_wav()),
                "downloadPath": bridge.AUDIO_PATH_PREFIX + "audio-1",
            }
            result = bridge._validate_turn_result(
                json.loads(
                    turn_json(
                        "request-1",
                        kind="answer",
                        audio_status="ready",
                        files=[audio],
                    )
                ),
                "request-1",
                settings,
            )
            self.assertEqual(result.audio.files[0].id, "audio-1")

            invalid_payloads = [
                json.loads(
                    turn_json(
                        "request-1",
                        kind="answer",
                        audio_status="ready",
                    )
                ),
                json.loads(
                    turn_json(
                        "request-1",
                        kind="clarification",
                        audio_status="ready",
                        files=[audio],
                    )
                ),
                json.loads(
                    turn_json(
                        "request-1",
                        kind="answer",
                        audio_status="ready",
                        files=[
                            {
                                **audio,
                                "downloadPath": "https://evil.invalid/audio.wav",
                            }
                        ],
                    )
                ),
            ]
            for payload in invalid_payloads:
                with self.subTest(payload=payload):
                    with self.assertRaises(bridge.BridgeError):
                        bridge._validate_turn_result(
                            payload,
                            "request-1",
                            settings,
                        )

    def test_json_decoder_rejects_duplicates_and_non_objects(self) -> None:
        with self.assertRaises(bridge.BridgeError):
            bridge._decode_json_object(b'{"text":"one","text":"two"}')
        with self.assertRaises(bridge.BridgeError):
            bridge._decode_json_object(b"[]")

    def test_http_error_reads_slim_error_envelope(self) -> None:
        error = urllib.error.HTTPError(
            "http://slim.test/turn",
            409,
            "conflict",
            {"Content-Type": "application/json", "Retry-After": "2"},
            io.BytesIO(
                b'{"error":{"code":"turn_in_progress","message":"wait"}}'
            ),
        )
        parsed = bridge._bridge_http_error(error)
        self.assertEqual(parsed.status, 409)
        self.assertEqual(parsed.code, "turn_in_progress")
        self.assertEqual(parsed.retry_after, 2)

    def test_audio_download_reuses_bearer_and_creates_private_wav(self) -> None:
        wav = minimal_wav()
        with tempfile.TemporaryDirectory() as temporary:
            settings = make_settings(Path(temporary))
            client = bridge.BridgeClient(settings)
            opener = FakeOpener(
                [
                    FakeResponse(
                        wav,
                        content_type="audio/wav",
                        content_length=len(wav),
                    )
                ]
            )
            client._opener = opener  # type: ignore[assignment]
            path = client._download_audio(make_audio_file("audio-1", len(wav)))
            try:
                self.assertEqual(path.read_bytes(), wav)
                self.assertEqual(
                    stat.S_IMODE(path.stat().st_mode),
                    0o600,
                )
                request = opener.requests[0]
                self.assertEqual(
                    request.get_header("Authorization"),
                    "Bearer " + TOKEN,
                )
                self.assertEqual(
                    request.full_url,
                    "http://slim.test:3000"
                    + bridge.AUDIO_PATH_PREFIX
                    + "audio-1",
                )
            finally:
                path.unlink(missing_ok=True)

    def test_audio_download_rejects_type_size_wav_and_http_failures(self) -> None:
        wav = minimal_wav()
        with tempfile.TemporaryDirectory() as temporary:
            settings = make_settings(Path(temporary))
            cases: list[tuple[str, object, bridge.AudioFile]] = [
                (
                    "content type",
                    FakeResponse(wav, content_type="application/octet-stream"),
                    make_audio_file("audio-1", len(wav)),
                ),
                (
                    "oversize stream",
                    FakeResponse(
                        wav + (b"x" * (settings.max_audio_bytes + 1)),
                        content_type="audio/wav",
                    ),
                    make_audio_file("audio-2", len(wav)),
                ),
                (
                    "invalid wav",
                    FakeResponse(
                        b"x" * len(wav),
                        content_type="audio/wav",
                        content_length=len(wav),
                    ),
                    make_audio_file("audio-3", len(wav)),
                ),
                (
                    "http error",
                    urllib.error.HTTPError(
                        "http://slim.test/audio",
                        404,
                        "not found",
                        {"Content-Type": "application/json"},
                        io.BytesIO(b'{"code":"not_found"}'),
                    ),
                    make_audio_file("audio-4", len(wav)),
                ),
            ]
            for name, response, audio in cases:
                with self.subTest(name=name):
                    client = bridge.BridgeClient(settings)
                    client._opener = FakeOpener([response])  # type: ignore[assignment]
                    with self.assertRaises(bridge.BridgeError):
                        client._download_audio(audio)
            self.assertEqual(list(Path(temporary).glob("*.wav")), [])

    def test_audio_url_rejects_redirectable_or_cross_origin_paths(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            client = bridge.BridgeClient(make_settings(Path(temporary)))
            for path in (
                "https://evil.invalid/audio.wav",
                bridge.AUDIO_PATH_PREFIX + "audio-1?next=evil",
                "/different/audio-1",
            ):
                with self.subTest(path=path):
                    audio = bridge.AudioFile(
                        id="audio-1",
                        file_name="answer.wav",
                        byte_size=44,
                        download_path=path,
                    )
                    with self.assertRaises(bridge.BridgeError):
                        client._audio_url(audio)


class BridgeAsyncTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self) -> None:
        bridge._ACTIVE_CREDENTIALS.clear()
        await self._cancel_tracked_tasks()

    async def asyncTearDown(self) -> None:
        await self._cancel_tracked_tasks()
        bridge._ACTIVE_CREDENTIALS.clear()

    async def _cancel_tracked_tasks(self) -> None:
        tasks = list(bridge._TASKS)
        for task in tasks:
            if not task.done():
                task.cancel()
        if tasks:
            await asyncio.gather(*tasks, return_exceptions=True)
        bridge._TASKS.clear()

    async def _await_tracked_tasks(self) -> None:
        tasks = list(bridge._TASKS)
        if tasks:
            await asyncio.gather(*tasks)
        await asyncio.sleep(0)

    async def test_hook_only_intercepts_authorized_weixin_dm(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            settings = make_settings(Path(temporary))
            adapter = FakeAdapter()
            gateway = FakeGateway(adapter)
            event = make_event()

            with (
                mock.patch.object(
                    bridge,
                    "_resolve_settings_and_authorization",
                    return_value=(settings, True),
                ),
                mock.patch.object(
                    bridge,
                    "_run_turn",
                    new=mock.AsyncMock(),
                ) as run_turn,
            ):
                result = bridge.pre_gateway_dispatch(
                    event=event,
                    gateway=gateway,
                )
                self.assertEqual(
                    result,
                    {
                        "action": "skip",
                        "reason": bridge.PLUGIN_REASON,
                    },
                )
                await self._await_tracked_tasks()
                run_turn.assert_awaited_once()

            for resolved in ((settings, False), None):
                with (
                    self.subTest(resolved=resolved),
                    mock.patch.object(
                        bridge,
                        "_resolve_settings_and_authorization",
                        return_value=resolved,
                    ),
                ):
                    self.assertIsNone(
                        bridge.pre_gateway_dispatch(
                            event=event,
                            gateway=gateway,
                        )
                    )

            for excluded in (
                make_event(source=make_source(platform="telegram")),
                make_event(source=make_source(chat_type="group")),
                make_event(text="/status"),
            ):
                with self.subTest(excluded=excluded):
                    self.assertIsNone(
                        bridge.pre_gateway_dispatch(
                            event=excluded,
                            gateway=gateway,
                        )
                    )

    async def test_hook_returns_immediately_and_busy_message_is_explicit(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            settings = make_settings(Path(temporary))
            adapter = FakeAdapter()
            gateway = FakeGateway(adapter)
            release = asyncio.Event()
            started = asyncio.Event()

            async def blocked_turn(**kwargs: object) -> None:
                del kwargs
                started.set()
                await release.wait()

            with (
                mock.patch.object(
                    bridge,
                    "_resolve_settings_and_authorization",
                    return_value=(settings, True),
                ),
                mock.patch.object(
                    bridge,
                    "_run_turn",
                    side_effect=blocked_turn,
                ),
            ):
                first = bridge.pre_gateway_dispatch(
                    event=make_event(message_id="message-1"),
                    gateway=gateway,
                )
                self.assertEqual(first["action"], "skip")
                await asyncio.wait_for(started.wait(), timeout=1)

                second = bridge.pre_gateway_dispatch(
                    event=make_event(message_id="message-2"),
                    gateway=gateway,
                )
                self.assertEqual(
                    second,
                    {
                        "action": "skip",
                        "reason": bridge.PLUGIN_REASON + "_busy",
                    },
                )
                for _ in range(20):
                    if any(
                        event[-1] == bridge.BUSY_MESSAGE
                        for event in adapter.events
                        if event[0] == "text"
                    ):
                        break
                    await asyncio.sleep(0)
                self.assertIn(
                    ("text", "wx-chat-1", bridge.BUSY_MESSAGE),
                    adapter.events,
                )
                release.set()
                await self._await_tracked_tasks()
                self.assertNotIn(
                    settings.credential_key,
                    bridge._ACTIVE_CREDENTIALS,
                )

    async def test_typing_refreshes_and_stops_even_when_refresh_fails(self) -> None:
        for fail_typing in (False, True):
            with self.subTest(fail_typing=fail_typing):
                adapter = FakeAdapter()
                adapter.fail_typing = fail_typing
                stop = asyncio.Event()
                with mock.patch.object(
                    bridge,
                    "TYPING_INTERVAL_SECONDS",
                    0.005,
                ):
                    task = asyncio.create_task(
                        bridge._typing_loop(adapter, "wx-chat-1", stop)
                    )
                    await asyncio.sleep(0.018)
                    stop.set()
                    await asyncio.wait_for(task, timeout=1)
                typing_events = [
                    event for event in adapter.events if event[0] == "typing"
                ]
                self.assertGreaterEqual(len(typing_events), 2)
                self.assertEqual(
                    adapter.events[-1],
                    ("stop_typing", "wx-chat-1"),
                )

    async def test_turn_sends_full_text_then_ordered_audio_and_cleans_files(
        self,
    ) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            settings = make_settings(directory)
            adapter = FakeAdapter()
            gateway = FakeGateway(adapter)
            files = (
                make_audio_file("audio-1"),
                make_audio_file("audio-2"),
            )
            result = make_turn_result(files=files)
            downloaded: list[Path] = []

            class FakeClient:
                def __init__(self, ignored_settings: object) -> None:
                    del ignored_settings

                async def turn(
                    self,
                    request_id: str,
                    session_id: str,
                    text: str,
                ) -> bridge.TurnResult:
                    self.request = (request_id, session_id, text)
                    await asyncio.sleep(0)
                    return result

                async def download(self, audio: bridge.AudioFile) -> Path:
                    path = directory / f"temporary-{audio.id}.wav"
                    path.write_bytes(minimal_wav())
                    downloaded.append(path)
                    return path

            with mock.patch.object(bridge, "BridgeClient", FakeClient):
                await bridge._run_turn(
                    event=make_event(),
                    gateway=gateway,
                    session_store=None,
                    adapter=adapter,
                    settings=settings,
                )

            delivered = [
                event for event in adapter.events if event[0] in {"text", "voice"}
            ]
            self.assertEqual(
                delivered,
                [
                    ("text", "wx-chat-1", "完整文字答案"),
                    (
                        "voice",
                        "wx-chat-1",
                        "temporary-audio-1.wav",
                        "语音答案 1/2",
                    ),
                    (
                        "voice",
                        "wx-chat-1",
                        "temporary-audio-2.wav",
                        "语音答案 2/2",
                    ),
                ],
            )
            self.assertTrue(
                any(event[0] == "typing" for event in adapter.events)
            )
            self.assertEqual(adapter.events[-1][0], "stop_typing")
            self.assertTrue(downloaded)
            self.assertTrue(all(not path.exists() for path in downloaded))

    async def test_audio_failure_keeps_text_cleans_partial_files_and_stops_typing(
        self,
    ) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            directory = Path(temporary)
            settings = make_settings(directory)
            adapter = FakeAdapter()
            gateway = FakeGateway(adapter)
            first_path = directory / "first.wav"
            result = make_turn_result(
                files=(
                    make_audio_file("audio-1"),
                    make_audio_file("audio-2"),
                )
            )

            class FakeClient:
                def __init__(self, ignored_settings: object) -> None:
                    del ignored_settings

                async def turn(self, *args: object) -> bridge.TurnResult:
                    del args
                    return result

                async def download(self, audio: bridge.AudioFile) -> Path:
                    if audio.id == "audio-2":
                        raise bridge.BridgeError("download failed")
                    first_path.write_bytes(minimal_wav())
                    return first_path

            with mock.patch.object(bridge, "BridgeClient", FakeClient):
                await bridge._run_turn(
                    event=make_event(),
                    gateway=gateway,
                    session_store=None,
                    adapter=adapter,
                    settings=settings,
                )

            text_events = [
                event for event in adapter.events if event[0] == "text"
            ]
            self.assertEqual(text_events[0][-1], "完整文字答案")
            self.assertEqual(
                text_events[-1][-1],
                bridge.AUDIO_DELIVERY_FAILED_MESSAGE,
            )
            self.assertFalse(first_path.exists())
            self.assertEqual(adapter.events[-1][0], "stop_typing")

    async def test_audio_unavailable_still_sends_complete_text(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            settings = make_settings(Path(temporary))
            adapter = FakeAdapter()
            gateway = FakeGateway(adapter)
            result = make_turn_result(
                audio_status="unavailable",
                files=(),
                audio_code="tts_disabled",
            )

            class FakeClient:
                def __init__(self, ignored_settings: object) -> None:
                    del ignored_settings

                async def turn(self, *args: object) -> bridge.TurnResult:
                    del args
                    return result

            with mock.patch.object(bridge, "BridgeClient", FakeClient):
                await bridge._run_turn(
                    event=make_event(),
                    gateway=gateway,
                    session_store=None,
                    adapter=adapter,
                    settings=settings,
                )

            delivered = [
                event for event in adapter.events if event[0] in {"text", "voice"}
            ]
            self.assertEqual(
                delivered,
                [
                    ("text", "wx-chat-1", "完整文字答案"),
                    (
                        "text",
                        "wx-chat-1",
                        bridge.AUDIO_UNAVAILABLE_MESSAGE,
                    ),
                ],
            )
            self.assertEqual(adapter.events[-1][0], "stop_typing")

    async def test_turn_failure_sends_safe_error_and_stops_typing(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            settings = make_settings(Path(temporary))
            adapter = FakeAdapter()
            gateway = FakeGateway(adapter)

            class FailingClient:
                def __init__(self, ignored_settings: object) -> None:
                    del ignored_settings

                async def turn(self, *args: object) -> bridge.TurnResult:
                    del args
                    raise bridge.BridgeError("secret provider detail")

            with mock.patch.object(bridge, "BridgeClient", FailingClient):
                await bridge._run_turn(
                    event=make_event(),
                    gateway=gateway,
                    session_store=None,
                    adapter=adapter,
                    settings=settings,
                )

            self.assertIn(
                (
                    "text",
                    "wx-chat-1",
                    bridge.BRIDGE_UNAVAILABLE_MESSAGE,
                ),
                adapter.events,
            )
            self.assertFalse(
                any(
                    "secret provider detail" in str(part)
                    for event in adapter.events
                    for part in event
                )
            )
            self.assertEqual(adapter.events[-1][0], "stop_typing")

    async def test_network_and_server_retries_reuse_request_id(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            client = bridge.BridgeClient(make_settings(Path(temporary)))
            post = mock.Mock(
                side_effect=[
                    urllib.error.URLError("temporary"),
                    bridge.BridgeHTTPError(503, "unavailable"),
                    turn_json("request-1"),
                ]
            )
            client._post_turn = post  # type: ignore[method-assign]
            with mock.patch.object(
                bridge,
                "_sleep_with_deadline",
                new=mock.AsyncMock(),
            ) as sleep:
                result = await client.turn(
                    "request-1",
                    "session-1",
                    "hello",
                )

            self.assertEqual(result.request_id, "request-1")
            self.assertEqual(post.call_count, 3)
            self.assertEqual(sleep.await_count, 2)
            for call in post.call_args_list:
                self.assertEqual(call.args[0]["requestId"], "request-1")
                self.assertEqual(call.args[0]["sessionId"], "session-1")
                self.assertEqual(call.args[0]["text"], "hello")

    async def test_client_does_not_retry_normal_4xx(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            client = bridge.BridgeClient(make_settings(Path(temporary)))
            post = mock.Mock(
                side_effect=bridge.BridgeHTTPError(400, "invalid_request")
            )
            client._post_turn = post  # type: ignore[method-assign]
            with self.assertRaises(bridge.BridgeHTTPError):
                await client.turn("request-1", "session-1", "hello")
            post.assert_called_once()

    async def test_turn_in_progress_polls_with_same_request(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            client = bridge.BridgeClient(make_settings(Path(temporary)))
            post = mock.Mock(
                side_effect=[
                    bridge.BridgeHTTPError(
                        409,
                        "turn_in_progress",
                        retry_after=0.25,
                    ),
                    turn_json("request-1"),
                ]
            )
            client._post_turn = post  # type: ignore[method-assign]
            with mock.patch.object(
                bridge,
                "_sleep_with_deadline",
                new=mock.AsyncMock(),
            ) as sleep:
                await client.turn("request-1", "session-1", "hello")
            self.assertEqual(post.call_count, 2)
            sleep.assert_awaited_once()
            self.assertEqual(sleep.await_args.args[0], 0.25)
            self.assertTrue(
                all(
                    call.args[0]["requestId"] == "request-1"
                    for call in post.call_args_list
                )
            )


class PluginRegistrationTests(unittest.TestCase):
    def test_register_uses_supported_pre_dispatch_hook(self) -> None:
        from integrations.hermes.slim_restaurant_bridge import register

        context = SimpleNamespace(register_hook=mock.Mock())
        register(context)
        context.register_hook.assert_called_once_with(
            "pre_gateway_dispatch",
            bridge.pre_gateway_dispatch,
        )


if __name__ == "__main__":
    unittest.main()
