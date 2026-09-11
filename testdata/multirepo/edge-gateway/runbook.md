# edge runbook

## Symptom: 5xx at the edge

1. Check whether `api` (the upstream this gateway depends on) is healthy
   first — most edge 5xx are the upstream's, surfaced here.
2. If the upstream is healthy, check the edge gateway's own deploy history.
