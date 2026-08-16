# Keep Weixin restaurant business state in La4RainGPT

Hermes will remain the authenticated Weixin transport and typing-indicator
gateway, while La4RainGPT owns the bound User, restaurant conversation,
three-question clarification contract, provider call, answer text, and TTS
files behind a narrow server-to-server API. This avoids duplicating guidance
state, profile facts, quotas, and speech policy in Hermes; the trade-off is
that Weixin restaurant turns depend on both services, so text must fail
independently from best-effort audio and the Hermes plugin must preserve
La4RainGPT's response verbatim.
