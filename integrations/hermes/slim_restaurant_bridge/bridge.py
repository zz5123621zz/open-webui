"""Authorized Weixin-to-slim restaurant bridge for Hermes Agent 0.19.x.

The pre-dispatch hook is intentionally synchronous because Hermes invokes
plugin hooks synchronously. It performs only bounded local checks, reserves
the credential, schedules an asyncio task, and returns ``skip``. The scheduled
task owns typing, the bridge HTTP exchange, delivery, and cleanup.
"""

from __future__ import annotations

import asyncio
import hashlib
import json
import logging
import os
import re
import socket
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Mapping, Optional


LOGGER = logging.getLogger(__name__)

PLUGIN_REASON = "slim_restaurant_bridge"
TURN_PATH = "/api/v1/integrations/hermes/restaurant/turn"
AUDIO_PATH_PREFIX = "/api/v1/integrations/hermes/restaurant/audio/"
TYPING_INTERVAL_SECONDS = 2.0
TYPING_CALL_TIMEOUT_SECONDS = 1.5
MAX_TURN_RESPONSE_BYTES = 4 * 1024 * 1024
MAX_AUDIO_FILES = 32
MAX_ERROR_BODY_BYTES = 64 * 1024
MAX_TEXT_BYTES = 2 * 1024 * 1024
MAX_CODE_CHARS = 100
TOKEN_PATTERN = re.compile(r"^hbr_[A-Za-z0-9_-]{43}$")
AUDIO_ID_PATTERN = re.compile(r"^[A-Za-z0-9_-]{1,128}$")

BUSY_MESSAGE = "上一条消息仍在处理中，请稍候，完成后再发送下一条。"
BRIDGE_UNAVAILABLE_MESSAGE = "餐饮问答服务暂时不可用，请稍后重试。"
AUDIO_UNAVAILABLE_MESSAGE = "文字答案已发送，但语音暂不可用。"
AUDIO_DELIVERY_FAILED_MESSAGE = "文字答案已发送，但语音附件发送失败，请稍后重试。"

_ACTIVE_CREDENTIALS: set[bytes] = set()
_TASKS: set[asyncio.Task[Any]] = set()


class BridgeError(RuntimeError):
    """Safe internal bridge failure."""


class BridgeHTTPError(BridgeError):
    def __init__(
        self,
        status: int,
        code: str = "",
        retry_after: float = 0,
    ) -> None:
        super().__init__(f"bridge HTTP {status} ({code or 'unknown'})")
        self.status = status
        self.code = code
        self.retry_after = retry_after


class DeliveryError(BridgeError):
    """Hermes adapter reported a failed outbound delivery."""


@dataclass(frozen=True)
class BridgeSettings:
    base_url: str
    token: str = field(repr=False)
    timeout_seconds: float
    max_audio_bytes: int
    max_total_audio_bytes: int
    media_dir: Path
    profile_identity: str

    @property
    def credential_key(self) -> bytes:
        return hashlib.sha256(self.token.encode("utf-8")).digest()


@dataclass(frozen=True)
class AudioFile:
    id: str
    file_name: str
    byte_size: int
    download_path: str


@dataclass(frozen=True)
class AudioResult:
    status: str
    code: str
    files: tuple[AudioFile, ...]


@dataclass(frozen=True)
class TurnResult:
    request_id: str
    kind: str
    text: str
    audio: AudioResult


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        del req, fp, code, msg, headers, newurl
        return None


class BridgeClient:
    def __init__(self, settings: BridgeSettings) -> None:
        self._settings = settings
        self._opener = urllib.request.build_opener(_NoRedirect())
        parsed = urllib.parse.urlsplit(settings.base_url)
        self._origin = (parsed.scheme.lower(), parsed.netloc.lower())

    async def turn(
        self,
        request_id: str,
        session_id: str,
        text: str,
    ) -> TurnResult:
        payload = {
            "requestId": request_id,
            "sessionId": session_id,
            "text": text,
        }
        deadline = time.monotonic() + self._settings.timeout_seconds
        transient_failures = 0
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise BridgeError("bridge turn timed out")
            try:
                raw = await asyncio.to_thread(
                    self._post_turn,
                    payload,
                    remaining,
                )
                decoded = _decode_json_object(raw)
                return _validate_turn_result(
                    decoded,
                    request_id,
                    self._settings,
                )
            except BridgeHTTPError as exc:
                if exc.status == 409 and exc.code == "turn_in_progress":
                    delay = exc.retry_after if exc.retry_after > 0 else 2.0
                    await _sleep_with_deadline(delay, deadline)
                    continue
                if exc.status < 500 or transient_failures >= 2:
                    raise
                transient_failures += 1
                await _sleep_with_deadline(
                    0.5 * (2 ** (transient_failures - 1)),
                    deadline,
                )
            except (
                TimeoutError,
                socket.timeout,
                urllib.error.URLError,
                OSError,
            ) as exc:
                if transient_failures >= 2:
                    raise BridgeError("bridge network request failed") from exc
                transient_failures += 1
                await _sleep_with_deadline(
                    0.5 * (2 ** (transient_failures - 1)),
                    deadline,
                )

    async def download(self, audio: AudioFile) -> Path:
        return await asyncio.to_thread(self._download_audio, audio)

    def _post_turn(self, payload: Mapping[str, Any], timeout: float) -> bytes:
        body = json.dumps(
            payload,
            ensure_ascii=False,
            separators=(",", ":"),
        ).encode("utf-8")
        request = urllib.request.Request(
            self._settings.base_url + TURN_PATH,
            data=body,
            method="POST",
            headers={
                **self._headers("application/json"),
                "Content-Type": "application/json",
            },
        )
        try:
            with self._opener.open(request, timeout=max(0.1, timeout)) as response:
                if response.status != 200:
                    raise BridgeHTTPError(response.status)
                content_type = (
                    response.headers.get("Content-Type", "")
                    .partition(";")[0]
                    .strip()
                    .lower()
                )
                if content_type != "application/json":
                    raise BridgeError("turn response has an invalid content type")
                return _read_limited(response, MAX_TURN_RESPONSE_BYTES)
        except urllib.error.HTTPError as exc:
            raise _bridge_http_error(exc) from exc

    def _download_audio(self, audio: AudioFile) -> Path:
        url = self._audio_url(audio)
        request = urllib.request.Request(
            url,
            method="GET",
            headers=self._headers("audio/wav"),
        )
        try:
            with self._opener.open(
                request,
                timeout=self._settings.timeout_seconds,
            ) as response:
                if response.status != 200:
                    raise BridgeHTTPError(response.status)
                content_type = (
                    response.headers.get("Content-Type", "")
                    .partition(";")[0]
                    .strip()
                    .lower()
                )
                if content_type != "audio/wav":
                    raise BridgeError("audio response has an invalid content type")
                content_length = _content_length(response.headers)
                if content_length is not None and content_length != audio.byte_size:
                    raise BridgeError("audio content length does not match metadata")
                data = _read_limited(response, self._settings.max_audio_bytes)
        except urllib.error.HTTPError as exc:
            raise _bridge_http_error(exc) from exc
        if len(data) != audio.byte_size:
            raise BridgeError("audio body length does not match metadata")
        _validate_wav(data)
        return _write_private_audio(self._settings.media_dir, data)

    def _audio_url(self, audio: AudioFile) -> str:
        parsed = urllib.parse.urlsplit(audio.download_path)
        if (
            parsed.scheme
            or parsed.netloc
            or parsed.query
            or parsed.fragment
            or parsed.path != AUDIO_PATH_PREFIX + audio.id
        ):
            raise BridgeError("audio download path is invalid")
        base = urllib.parse.urlsplit(self._settings.base_url)
        return urllib.parse.urlunsplit(
            (base.scheme, base.netloc, parsed.path, "", "")
        )

    def _headers(self, accept: str) -> dict[str, str]:
        return {
            "Accept": accept,
            "Authorization": "Bearer " + self._settings.token,
            "User-Agent": "slim-restaurant-bridge/1.0",
        }


def pre_gateway_dispatch(
    *,
    event: Any,
    gateway: Any,
    session_store: Any = None,
    **_: Any,
) -> Optional[dict[str, str]]:
    """Hermes synchronous pre-dispatch hook."""

    source = getattr(event, "source", None)
    if not _eligible_event(event, source):
        return None
    resolved = _resolve_settings_and_authorization(gateway, source)
    if resolved is None:
        return None
    settings, authorized = resolved
    if not authorized:
        # Let Hermes' normal authorization and pairing path handle this user.
        return None
    adapter_factory = getattr(gateway, "_adapter_for_source", None)
    if not callable(adapter_factory):
        return None
    adapter = adapter_factory(source)
    if adapter is None:
        return None

    loop = asyncio.get_running_loop()
    credential_key = settings.credential_key
    if credential_key in _ACTIVE_CREDENTIALS:
        _track_task(loop.create_task(_send_busy(adapter, source.chat_id)))
        return {"action": "skip", "reason": PLUGIN_REASON + "_busy"}

    _ACTIVE_CREDENTIALS.add(credential_key)
    try:
        task = loop.create_task(
            _run_reserved_turn(
                event=event,
                gateway=gateway,
                session_store=session_store,
                adapter=adapter,
                settings=settings,
            )
        )
    except BaseException:
        _ACTIVE_CREDENTIALS.discard(credential_key)
        raise
    _track_task(task)
    return {"action": "skip", "reason": PLUGIN_REASON}


def _eligible_event(event: Any, source: Any) -> bool:
    if source is None:
        return False
    platform = getattr(source, "platform", None)
    platform_name = getattr(platform, "value", platform)
    if platform_name != "weixin" or getattr(source, "chat_type", None) != "dm":
        return False
    if bool(getattr(event, "internal", False)):
        return False
    message_type = getattr(event, "message_type", None)
    message_type_name = getattr(message_type, "value", message_type)
    if message_type_name not in (None, "text"):
        return False
    text = getattr(event, "text", None)
    if not isinstance(text, str) or not text.strip():
        return False
    if text.lstrip().startswith("/"):
        return False
    command_getter = getattr(event, "get_command", None)
    if callable(command_getter):
        try:
            if command_getter():
                return False
        except Exception:
            return False
    return True


def _resolve_settings_and_authorization(
    gateway: Any,
    source: Any,
) -> Optional[tuple[BridgeSettings, bool]]:
    """Resolve profile-scoped values without leaking across multiplex profiles."""

    try:
        from agent import secret_scope

        resolver = getattr(gateway, "_resolve_profile_home_for_source", None)
        if callable(resolver):
            profile_home = Path(resolver(source))
        else:
            from hermes_constants import get_hermes_home

            profile_home = Path(get_hermes_home())
        secrets = secret_scope.build_profile_secret_scope(profile_home)
        scope_token = secret_scope.set_secret_scope(secrets)
        try:
            token = str(
                secret_scope.get_secret(
                    "SLIM_RESTAURANT_BRIDGE_TOKEN",
                    "",
                )
                or ""
            ).strip()
            base_url = str(
                secret_scope.get_secret(
                    "SLIM_RESTAURANT_BRIDGE_URL",
                    "",
                )
                or ""
            ).strip()
            timeout = _env_float(
                secret_scope.get_secret(
                    "SLIM_RESTAURANT_BRIDGE_TIMEOUT_SECONDS",
                    "900",
                ),
                default=900,
                minimum=10,
                maximum=1800,
            )
            max_audio = _env_int(
                secret_scope.get_secret(
                    "SLIM_RESTAURANT_BRIDGE_MAX_AUDIO_BYTES",
                    str(25 * 1024 * 1024),
                ),
                default=25 * 1024 * 1024,
                minimum=1024,
                maximum=50 * 1024 * 1024,
            )
            max_total_audio = _env_int(
                secret_scope.get_secret(
                    "SLIM_RESTAURANT_BRIDGE_MAX_TOTAL_AUDIO_BYTES",
                    str(100 * 1024 * 1024),
                ),
                default=100 * 1024 * 1024,
                minimum=max_audio,
                maximum=200 * 1024 * 1024,
            )
            authorizer = getattr(gateway, "_is_user_authorized", None)
            authorized = bool(callable(authorizer) and authorizer(source))
        finally:
            secret_scope.reset_secret_scope(scope_token)
    except Exception:
        LOGGER.warning(
            "slim restaurant bridge profile settings could not be resolved",
            exc_info=True,
        )
        return None

    normalized_url = _normalize_base_url(base_url)
    if not TOKEN_PATTERN.fullmatch(token) or normalized_url is None:
        return None
    profile_identity = hashlib.sha256(
        str(profile_home.resolve()).encode("utf-8")
    ).hexdigest()
    return (
        BridgeSettings(
            base_url=normalized_url,
            token=token,
            timeout_seconds=timeout,
            max_audio_bytes=max_audio,
            max_total_audio_bytes=max_total_audio,
            media_dir=profile_home / "media" / "slim-restaurant-bridge",
            profile_identity=profile_identity,
        ),
        authorized,
    )


async def _run_reserved_turn(
    *,
    event: Any,
    gateway: Any,
    session_store: Any,
    adapter: Any,
    settings: BridgeSettings,
) -> None:
    try:
        await _run_turn(
            event=event,
            gateway=gateway,
            session_store=session_store,
            adapter=adapter,
            settings=settings,
        )
    finally:
        _ACTIVE_CREDENTIALS.discard(settings.credential_key)


async def _run_turn(
    *,
    event: Any,
    gateway: Any,
    session_store: Any,
    adapter: Any,
    settings: BridgeSettings,
) -> None:
    source = event.source
    stop_typing = asyncio.Event()
    typing_task = asyncio.create_task(
        _typing_loop(adapter, source.chat_id, stop_typing)
    )
    temporary_files: list[Path] = []
    text_delivered = False
    try:
        session_id = await _get_session_id(gateway, session_store, source)
        request_id = _request_id(event, source, settings.profile_identity)
        result = await BridgeClient(settings).turn(
            request_id,
            session_id,
            event.text,
        )
        await _checked_send(adapter.send(source.chat_id, result.text))
        text_delivered = True

        if result.audio.status == "unavailable":
            await _best_effort_send(
                adapter,
                source.chat_id,
                AUDIO_UNAVAILABLE_MESSAGE,
            )
            return
        if result.audio.status != "ready":
            return

        client = BridgeClient(settings)
        try:
            for index, audio in enumerate(result.audio.files):
                path = await client.download(audio)
                temporary_files.append(path)
                caption = "语音答案"
                if len(result.audio.files) > 1:
                    caption = f"语音答案 {index + 1}/{len(result.audio.files)}"
                await _checked_send(
                    adapter.send_voice(
                        chat_id=source.chat_id,
                        audio_path=str(path),
                        caption=caption,
                    )
                )
        except asyncio.CancelledError:
            raise
        except Exception:
            LOGGER.warning(
                "slim restaurant bridge audio delivery failed",
                exc_info=True,
            )
            await _best_effort_send(
                adapter,
                source.chat_id,
                AUDIO_DELIVERY_FAILED_MESSAGE,
            )
    except asyncio.CancelledError:
        raise
    except Exception:
        LOGGER.warning("slim restaurant bridge turn failed", exc_info=True)
        if not text_delivered:
            await _best_effort_send(
                adapter,
                source.chat_id,
                BRIDGE_UNAVAILABLE_MESSAGE,
            )
    finally:
        for path in temporary_files:
            try:
                path.unlink(missing_ok=True)
            except OSError:
                LOGGER.warning(
                    "slim restaurant bridge temporary audio cleanup failed",
                    exc_info=True,
                )
        stop_typing.set()
        try:
            await asyncio.wait_for(typing_task, timeout=3.0)
        except asyncio.TimeoutError:
            typing_task.cancel()
            await asyncio.gather(typing_task, return_exceptions=True)


async def _get_session_id(
    gateway: Any,
    session_store: Any,
    source: Any,
) -> str:
    async_store = getattr(gateway, "async_session_store", None)
    if async_store is not None:
        entry = await async_store.get_or_create_session(source)
    elif session_store is not None:
        entry = await asyncio.to_thread(
            session_store.get_or_create_session,
            source,
        )
    else:
        raise BridgeError("Hermes session store is unavailable")
    session_id = str(getattr(entry, "session_id", "") or "").strip()
    if not session_id or len(session_id.encode("utf-8")) > 128:
        raise BridgeError("Hermes session id is invalid")
    return session_id


async def _typing_loop(
    adapter: Any,
    chat_id: str,
    stop_event: asyncio.Event,
) -> None:
    try:
        while not stop_event.is_set():
            try:
                await asyncio.wait_for(
                    adapter.send_typing(chat_id),
                    timeout=TYPING_CALL_TIMEOUT_SECONDS,
                )
            except asyncio.CancelledError:
                raise
            except Exception:
                LOGGER.debug("slim restaurant bridge typing refresh failed")
            try:
                await asyncio.wait_for(
                    stop_event.wait(),
                    timeout=TYPING_INTERVAL_SECONDS,
                )
            except asyncio.TimeoutError:
                continue
    except asyncio.CancelledError:
        pass
    finally:
        try:
            await adapter.stop_typing(chat_id)
        except Exception:
            LOGGER.debug("slim restaurant bridge typing stop failed")


async def _send_busy(adapter: Any, chat_id: str) -> None:
    await _best_effort_send(adapter, chat_id, BUSY_MESSAGE)


async def _best_effort_send(adapter: Any, chat_id: str, text: str) -> None:
    try:
        await _checked_send(adapter.send(chat_id, text))
    except Exception:
        LOGGER.warning("slim restaurant bridge status delivery failed")


async def _checked_send(awaitable: Any) -> None:
    result = await awaitable
    if result is None:
        return
    success = getattr(result, "success", True)
    if not success:
        raise DeliveryError("Hermes adapter rejected outbound delivery")


def _request_id(event: Any, source: Any, profile_identity: str) -> str:
    message_id = str(getattr(event, "message_id", "") or "").strip()
    if not message_id:
        return "wx_" + uuid.uuid4().hex
    material = "\x00".join(
        (
            profile_identity,
            str(getattr(source, "chat_id", "") or ""),
            message_id,
        )
    )
    return "wx_" + hashlib.sha256(material.encode("utf-8")).hexdigest()


def _validate_turn_result(
    payload: Mapping[str, Any],
    request_id: str,
    settings: BridgeSettings,
) -> TurnResult:
    if payload.get("requestId") != request_id:
        raise BridgeError("turn response request id does not match")
    kind = payload.get("kind")
    if kind not in {"clarification", "task_brief", "answer"}:
        raise BridgeError("turn response kind is invalid")
    text = payload.get("text")
    if (
        not isinstance(text, str)
        or not text.strip()
        or len(text.encode("utf-8")) > MAX_TEXT_BYTES
    ):
        raise BridgeError("turn response text is invalid")
    raw_audio = payload.get("audio")
    if not isinstance(raw_audio, dict):
        raise BridgeError("turn response audio is invalid")
    status = raw_audio.get("status")
    if status not in {"not_applicable", "ready", "unavailable"}:
        raise BridgeError("turn response audio status is invalid")
    code = raw_audio.get("code", "")
    if not isinstance(code, str) or len(code) > MAX_CODE_CHARS:
        raise BridgeError("turn response audio code is invalid")
    raw_files = raw_audio.get("files")
    if not isinstance(raw_files, list) or len(raw_files) > MAX_AUDIO_FILES:
        raise BridgeError("turn response audio files are invalid")

    files: list[AudioFile] = []
    total_bytes = 0
    seen_ids: set[str] = set()
    for item in raw_files:
        if not isinstance(item, dict):
            raise BridgeError("turn response audio file is invalid")
        audio_id = item.get("id")
        file_name = item.get("fileName")
        content_type = item.get("contentType")
        byte_size = item.get("byteSize")
        download_path = item.get("downloadPath")
        if (
            not isinstance(audio_id, str)
            or not AUDIO_ID_PATTERN.fullmatch(audio_id)
            or audio_id in seen_ids
            or not isinstance(file_name, str)
            or not file_name.lower().endswith(".wav")
            or len(file_name) > 255
            or content_type != "audio/wav"
            or isinstance(byte_size, bool)
            or not isinstance(byte_size, int)
            or byte_size <= 44
            or byte_size > settings.max_audio_bytes
            or not isinstance(download_path, str)
            or download_path != AUDIO_PATH_PREFIX + audio_id
        ):
            raise BridgeError("turn response audio file metadata is invalid")
        seen_ids.add(audio_id)
        total_bytes += byte_size
        if total_bytes > settings.max_total_audio_bytes:
            raise BridgeError("turn response total audio size is too large")
        files.append(
            AudioFile(
                id=audio_id,
                file_name=file_name,
                byte_size=byte_size,
                download_path=download_path,
            )
        )

    if status == "ready" and not files:
        raise BridgeError("ready audio response has no files")
    if status != "ready" and files:
        raise BridgeError("non-ready audio response contains files")
    if kind == "answer" and status == "not_applicable":
        raise BridgeError("answer audio status cannot be not_applicable")
    if kind != "answer" and status != "not_applicable":
        raise BridgeError("non-answer response unexpectedly contains audio")
    return TurnResult(
        request_id=request_id,
        kind=kind,
        text=text,
        audio=AudioResult(
            status=status,
            code=code,
            files=tuple(files),
        ),
    )


def _normalize_base_url(value: str) -> Optional[str]:
    if not value:
        return None
    parsed = urllib.parse.urlsplit(value)
    if (
        parsed.scheme.lower() not in {"http", "https"}
        or not parsed.hostname
        or parsed.username is not None
        or parsed.password is not None
        or parsed.query
        or parsed.fragment
        or parsed.path not in ("", "/")
    ):
        return None
    return urllib.parse.urlunsplit(
        (parsed.scheme.lower(), parsed.netloc, "", "", "")
    ).rstrip("/")


def _bridge_http_error(exc: urllib.error.HTTPError) -> BridgeHTTPError:
    raw = b""
    try:
        raw = _read_limited(exc, MAX_ERROR_BODY_BYTES)
    except Exception:
        pass
    code = ""
    try:
        decoded = _decode_json_object(raw)
        candidate = decoded.get("code")
        if not isinstance(candidate, str):
            error = decoded.get("error")
            if isinstance(error, dict):
                candidate = error.get("code")
        if isinstance(candidate, str) and len(candidate) <= MAX_CODE_CHARS:
            code = candidate
    except BridgeError:
        pass
    return BridgeHTTPError(
        status=int(exc.code),
        code=code,
        retry_after=_retry_after_seconds(exc.headers),
    )


def _decode_json_object(raw: bytes) -> Mapping[str, Any]:
    try:
        value = json.loads(
            raw.decode("utf-8"),
            object_pairs_hook=_reject_duplicate_keys,
        )
    except (UnicodeDecodeError, json.JSONDecodeError, BridgeError) as exc:
        raise BridgeError("bridge returned invalid JSON") from exc
    if not isinstance(value, dict):
        raise BridgeError("bridge returned a non-object JSON response")
    return value


def _reject_duplicate_keys(
    pairs: list[tuple[str, Any]],
) -> dict[str, Any]:
    result: dict[str, Any] = {}
    for key, value in pairs:
        if key in result:
            raise BridgeError("bridge JSON contains duplicate keys")
        result[key] = value
    return result


def _read_limited(stream: Any, maximum: int) -> bytes:
    content_length = _content_length(getattr(stream, "headers", None))
    if content_length is not None and content_length > maximum:
        raise BridgeError("bridge response is too large")
    chunks: list[bytes] = []
    total = 0
    while True:
        chunk = stream.read(min(64 * 1024, maximum - total + 1))
        if not chunk:
            break
        total += len(chunk)
        if total > maximum:
            raise BridgeError("bridge response is too large")
        chunks.append(chunk)
    return b"".join(chunks)


def _content_length(headers: Any) -> Optional[int]:
    if headers is None:
        return None
    raw = headers.get("Content-Length")
    if raw is None:
        return None
    try:
        value = int(raw)
    except (TypeError, ValueError) as exc:
        raise BridgeError("bridge content length is invalid") from exc
    if value < 0:
        raise BridgeError("bridge content length is invalid")
    return value


def _retry_after_seconds(headers: Any) -> float:
    if headers is None:
        return 0
    raw = headers.get("Retry-After")
    try:
        value = float(raw)
    except (TypeError, ValueError):
        return 0
    return min(10.0, max(0.1, value))


def _validate_wav(data: bytes) -> None:
    if (
        len(data) <= 44
        or data[:4] != b"RIFF"
        or data[8:12] != b"WAVE"
        or int.from_bytes(data[4:8], "little") + 8 != len(data)
    ):
        raise BridgeError("audio body is not a complete WAV file")


def _write_private_audio(directory: Path, data: bytes) -> Path:
    directory.mkdir(mode=0o700, parents=True, exist_ok=True)
    try:
        directory.chmod(0o700)
    except OSError:
        pass
    descriptor, temporary_name = tempfile.mkstemp(
        prefix=".slim-audio-",
        suffix=".part",
        dir=directory,
    )
    temporary = Path(temporary_name)
    target = directory / ("slim-audio-" + uuid.uuid4().hex + ".wav")
    try:
        os.fchmod(descriptor, 0o600)
        with os.fdopen(descriptor, "wb", closefd=True) as handle:
            descriptor = -1
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, target)
        return target
    except BaseException:
        if descriptor >= 0:
            os.close(descriptor)
        temporary.unlink(missing_ok=True)
        target.unlink(missing_ok=True)
        raise


async def _sleep_with_deadline(delay: float, deadline: float) -> None:
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise BridgeError("bridge turn timed out")
    await asyncio.sleep(min(delay, remaining))


def _env_int(
    value: Any,
    *,
    default: int,
    minimum: int,
    maximum: int,
) -> int:
    try:
        parsed = int(str(value))
    except (TypeError, ValueError):
        return default
    return min(maximum, max(minimum, parsed))


def _env_float(
    value: Any,
    *,
    default: float,
    minimum: float,
    maximum: float,
) -> float:
    try:
        parsed = float(str(value))
    except (TypeError, ValueError):
        return default
    return min(maximum, max(minimum, parsed))


def _track_task(task: asyncio.Task[Any]) -> None:
    _TASKS.add(task)
    task.add_done_callback(_task_done)


def _task_done(task: asyncio.Task[Any]) -> None:
    _TASKS.discard(task)
    if task.cancelled():
        return
    try:
        error = task.exception()
    except Exception:
        LOGGER.warning("slim restaurant bridge background task failed")
        return
    if error is not None:
        LOGGER.warning(
            "slim restaurant bridge background task failed: %s",
            type(error).__name__,
        )
