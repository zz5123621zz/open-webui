# Decouple response jobs from browser connections without automatic restart retry

La4RainGPT owns an accepted response job independently of any browser SSE
subscription, persists its status and received durable evidence in SQLite, and
allows an authorized browser to reconnect or explicitly cancel it; a lost
subscription therefore never cancels CPA work. Jobs run in the existing Go
process and expose rate-limited SQLite snapshots rather than an in-memory
replay broker or separate queue service, while unfinished jobs found after an
application restart are marked
service-interrupted and never resubmitted automatically, trading transparent
restart recovery for low VPS overhead and protection against duplicate quota
use and tool side effects.
