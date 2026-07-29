"""Load the bridge through Hermes' real 0.19.x plugin manager.

This script is executed inside the pinned Hermes container image by CI. It
does not contact slim, Weixin, or any external service.
"""

from pathlib import Path

from hermes_cli.plugins import PluginManager


def main() -> None:
    plugin_dir = Path(__file__).resolve().parent
    manager = PluginManager()
    manifest = manager._parse_manifest(
        plugin_dir / "plugin.yaml",
        plugin_dir,
        "user",
        "",
    )
    if manifest is None:
        raise RuntimeError("Hermes rejected the bridge plugin manifest")

    manager._load_plugin(manifest)
    key = manifest.key or manifest.name
    loaded = manager._plugins.get(key)
    if loaded is None or not loaded.enabled or loaded.error:
        detail = loaded.error if loaded is not None else "plugin missing"
        raise RuntimeError(f"Hermes could not load the bridge plugin: {detail}")
    if "pre_gateway_dispatch" not in loaded.hooks_registered:
        raise RuntimeError("bridge pre_gateway_dispatch hook was not registered")
    callbacks = manager._hooks.get("pre_gateway_dispatch", [])
    if len(callbacks) != 1 or not callable(callbacks[0]):
        raise RuntimeError("bridge hook registration is incomplete")

    print("Hermes loaded slim-restaurant-bridge and registered its hook")


if __name__ == "__main__":
    main()
