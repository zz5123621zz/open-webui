# slim restaurant bridge plugin

This directory is a Hermes Agent 0.19.x user plugin. It is source material
only until the operator copies it into the intended Hermes installation and
enables it; repository tests do not install or deploy it.

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
