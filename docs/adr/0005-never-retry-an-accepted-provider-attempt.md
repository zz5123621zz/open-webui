# Never transparently retry an accepted provider attempt

La4RainGPT may retry once without progressive summary delivery only when the
provider explicitly rejects the experimental field before returning a
successful response or any stream event. Once an attempt is accepted, failures
remain visible as partial or failed responses and retry is manual, trading
automatic recovery for protection against duplicate quota consumption,
searches, image generation, and other tool side effects.
