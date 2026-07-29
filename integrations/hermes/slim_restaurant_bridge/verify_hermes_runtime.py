"""Load the bridge through Hermes' real 0.19.x plugin/runtime contracts.

This script is executed inside the pinned Hermes container image by CI. It
does not contact slim, Weixin, or any external service.
"""

import inspect
import os
import shutil
import tempfile
from pathlib import Path


PLUGIN_NAME = "slim-restaurant-bridge"
PLUGIN_DIRECTORY = "slim_restaurant_bridge"


def _require_method(
    owner: type,
    name: str,
    parameters: set[str],
    *,
    asynchronous: bool,
) -> None:
    method = getattr(owner, name, None)
    if not callable(method):
        raise RuntimeError(f"Hermes runtime lacks {owner.__name__}.{name}")
    if asynchronous and not inspect.iscoroutinefunction(method):
        raise RuntimeError(f"Hermes runtime method {owner.__name__}.{name} is not async")
    available = set(inspect.signature(method).parameters)
    missing = parameters - available
    if missing:
        missing_names = ", ".join(sorted(missing))
        raise RuntimeError(
            f"Hermes runtime method {owner.__name__}.{name} lacks: "
            f"{missing_names}"
        )


def _verify_gateway_contract() -> None:
    from agent import secret_scope
    from gateway.authz_mixin import GatewayAuthorizationMixin
    from gateway.config import Platform
    from gateway.platforms.weixin import WeixinAdapter
    from gateway.profile_routing import parse_profile_routes
    from gateway.run import GatewayRunner
    from gateway.session import AsyncSessionStore

    _require_method(
        WeixinAdapter,
        "send",
        {"self", "chat_id", "content"},
        asynchronous=True,
    )
    _require_method(
        WeixinAdapter,
        "send_typing",
        {"self", "chat_id"},
        asynchronous=True,
    )
    _require_method(
        WeixinAdapter,
        "stop_typing",
        {"self", "chat_id"},
        asynchronous=True,
    )
    _require_method(
        WeixinAdapter,
        "send_voice",
        {"self", "chat_id", "audio_path", "caption"},
        asynchronous=True,
    )
    _require_method(
        GatewayRunner,
        "_adapter_for_source",
        {"self", "source"},
        asynchronous=False,
    )
    _require_method(
        GatewayRunner,
        "_resolve_profile_home_for_source",
        {"self", "source"},
        asynchronous=False,
    )
    _require_method(
        GatewayRunner,
        "_is_user_authorized",
        {"self", "source"},
        asynchronous=False,
    )
    _require_method(
        AsyncSessionStore,
        "get_or_create_session",
        {"self", "source"},
        asynchronous=True,
    )
    if not isinstance(
        inspect.getattr_static(GatewayRunner, "async_session_store"),
        property,
    ):
        raise RuntimeError("Hermes runtime lacks the async_session_store property")

    for name in (
        "build_profile_secret_scope",
        "set_secret_scope",
        "reset_secret_scope",
        "get_secret",
    ):
        if not callable(getattr(secret_scope, name, None)):
            raise RuntimeError(f"Hermes secret scope lacks {name}")

    voice_source = inspect.getsource(WeixinAdapter.send_voice)
    if "force_file_attachment=True" not in voice_source:
        raise RuntimeError(
            "Hermes Weixin send_voice no longer guarantees the documented "
            "file-attachment fallback"
        )

    routes = parse_profile_routes(
        [
            {
                "name": "father-restaurant",
                "platform": "weixin",
                "chat_id": "wx-father",
                "profile": "father-restaurant",
            }
        ]
    )
    if len(routes) != 1 or not routes[0].matches(
        "weixin",
        chat_id="wx-father",
    ):
        raise RuntimeError("Hermes rejected the documented Weixin profile route")

    resolver = GatewayAuthorizationMixin()
    default_adapter = object()
    father_adapter = object()
    resolver.adapters = {Platform.WEIXIN: default_adapter}
    resolver._profile_adapters = {
        "father-restaurant": {Platform.WEIXIN: father_adapter}
    }
    if (
        resolver._authorization_adapter(
            Platform.WEIXIN,
            "father-restaurant",
        )
        is not father_adapter
    ):
        raise RuntimeError("Hermes did not resolve the routed profile adapter")
    resolver._profile_adapters = {}
    if (
        resolver._authorization_adapter(
            Platform.WEIXIN,
            "father-restaurant",
        )
        is not None
    ):
        raise RuntimeError("Hermes no longer fails closed for a missing profile adapter")


def _load_as_installed_user_plugin(plugin_dir: Path) -> None:
    previous_home = os.environ.get("HERMES_HOME")
    previous_bundled = os.environ.get("HERMES_BUNDLED_PLUGINS")
    try:
        with tempfile.TemporaryDirectory(prefix="hermes-plugin-check-") as raw_home:
            home = Path(raw_home)
            bundled = home / "bundled"
            bundled.mkdir()
            installed = home / "plugins" / PLUGIN_DIRECTORY
            installed.parent.mkdir()
            shutil.copytree(
                plugin_dir,
                installed,
                ignore=shutil.ignore_patterns("__pycache__", "*.pyc"),
            )
            (home / "config.yaml").write_text(
                "plugins:\n"
                "  enabled:\n"
                f"    - {PLUGIN_NAME}\n",
                encoding="utf-8",
            )
            os.environ["HERMES_HOME"] = str(home)
            os.environ["HERMES_BUNDLED_PLUGINS"] = str(bundled)

            from hermes_cli.plugins import PluginManager

            manager = PluginManager()
            manager.discover_and_load()
            matches = [
                loaded
                for loaded in manager._plugins.values()
                if loaded.manifest.name == PLUGIN_NAME
            ]
            if len(matches) != 1:
                raise RuntimeError("Hermes did not discover exactly one bridge plugin")
            loaded = matches[0]
            if not loaded.enabled or loaded.error:
                detail = loaded.error or "plugin disabled"
                raise RuntimeError(
                    f"Hermes could not load the bridge plugin: {detail}"
                )
            if "pre_gateway_dispatch" not in loaded.hooks_registered:
                raise RuntimeError(
                    "bridge pre_gateway_dispatch hook was not registered"
                )
            callbacks = manager._hooks.get("pre_gateway_dispatch", [])
            if len(callbacks) != 1 or not callable(callbacks[0]):
                raise RuntimeError("bridge hook registration is incomplete")
    finally:
        if previous_home is None:
            os.environ.pop("HERMES_HOME", None)
        else:
            os.environ["HERMES_HOME"] = previous_home
        if previous_bundled is None:
            os.environ.pop("HERMES_BUNDLED_PLUGINS", None)
        else:
            os.environ["HERMES_BUNDLED_PLUGINS"] = previous_bundled


def main() -> None:
    plugin_dir = Path(__file__).resolve().parent
    _load_as_installed_user_plugin(plugin_dir)
    _verify_gateway_contract()

    print(
        "Hermes discovered and loaded slim-restaurant-bridge; "
        "plugin, profile routing, secret scope, session, typing, text, "
        "and WAV attachment contracts are compatible"
    )


if __name__ == "__main__":
    main()
