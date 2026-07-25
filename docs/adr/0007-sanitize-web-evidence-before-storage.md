# Sanitize web evidence before storage

La4RainGPT preserves provider-reported search queries, page titles, and useful
clickable HTTP(S) URLs, but removes URL credentials, fragments, tracking
parameters, and secret-like parameters before the evidence reaches durable
history. Request headers, cookies, internal provider fields, and encrypted
reasoning are never web evidence; this may make a rare signed link unusable but
prevents transient credentials and tracking data from becoming conversation
records.
