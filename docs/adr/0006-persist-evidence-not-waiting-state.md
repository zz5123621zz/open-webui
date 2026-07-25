# Persist provider evidence, not waiting state

La4RainGPT stores provider-authored summary sections and factual search or tool
events with stable identity and order, but does not store timers, heartbeats, or
waiting labels. Historical conversations therefore preserve useful upstream
evidence without presenting reconstructed activity as model output or filling
the database with ephemeral UI state.
