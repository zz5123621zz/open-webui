# slim restaurant bridge plugin

This directory is a Hermes Agent 0.19.x user plugin. It is source material
only until the operator copies the directory to
`$HERMES_HOME/plugins/slim_restaurant_bridge` in the gateway process's active
home and enables it; repository tests do not install or deploy it.

Hermes loads user plugins once for the gateway process. In multiplex mode the
plugin code and `plugins.enabled` entry therefore live in the active/default
gateway home, while the bridge URL and token below live only in the routed
restaurant profile's `.env`. Do not install a second code copy into each
secondary profile.

The plugin intercepts only authorized Weixin direct-message text in a profile
that contains both of these values in its profile-scoped `.env`:

```dotenv
SLIM_RESTAURANT_BRIDGE_URL=http://127.0.0.1:3001
SLIM_RESTAURANT_BRIDGE_TOKEN=hbr_REPLACE_WITH_THE_ONE_TIME_TOKEN
```

The loopback URL above is correct for this VPS because the Hermes container
uses `network_mode: host` and slim publishes `127.0.0.1:3001`. A deployment
with a shared Docker network must instead use that network's slim service name
and internal port.

Optional bounded settings:

```dotenv
SLIM_RESTAURANT_BRIDGE_TIMEOUT_SECONDS=900
SLIM_RESTAURANT_BRIDGE_MAX_AUDIO_BYTES=26214400
SLIM_RESTAURANT_BRIDGE_MAX_TOTAL_AUDIO_BYTES=104857600
```

Hermes must list `slim-restaurant-bridge` in `plugins.enabled` (the manifest
name, not the Python directory name). Do not enable
or copy the plugin until the slim Bridge credential has been issued for the
correct restaurant user and deployment has been explicitly approved.

For a routed secondary profile, Hermes 0.19.0 deliberately refuses to fall
back to the default profile's adapter. The target profile must have a live
Weixin adapter before enabling the route; otherwise the bridge must remain
disabled for that route.
