# Keep restaurant guidance inside the existing application process

La4RainGPT will implement restaurant guidance with the existing Go process,
SQLite store, response scheduler, SSE lifecycle, and browser-local draft state,
without Redis, a message queue, a workflow engine, a broker, or a separate
worker. This trades horizontal scaling and cross-restart job continuation for
minimal memory and operational overhead on the current small VPS; middleware
must be reconsidered separately if the service later runs multiple application
replicas or requires durable job resumption.
