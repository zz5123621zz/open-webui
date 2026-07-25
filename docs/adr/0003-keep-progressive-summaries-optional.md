# Keep progressive reasoning summaries optional

La4RainGPT may use a minimal CPA fork to enable progressive summary delivery,
but the application remains compatible with an unmodified CPA and can stop
requesting the experimental behavior through an audited administrator service
setting. A deployment-level hard disable remains available as an emergency
ceiling. The CPA customization stays in a separate repository and image,
accepts only the specific supported delivery value, and falls back to baseline
summary delivery when an upstream rejects it; this trades a small second
maintenance surface for earlier truthful progress without making chat
availability depend on a private field.
