# EV Charger API

A small event-driven Go service built to practice the stack from the Mapping Cloud internship posting: REST API, DynamoDB, EventBridge, SQS and Kubernetes.

```
                    POST /chargers
  client ──HTTP──▶  ┌──────────────┐  PutItem   ┌──────────────┐
                    │  api (Go)    │──────────▶ │  DynamoDB    │
                    │  2 pods, k3s │            │  ev-chargers │
                    └──────┬───────┘            └──────────────┘
                           │ PutEvents (ChargerCreated)
                           ▼
                    ┌──────────────┐   rule    ┌──────────────┐  after 3   ┌──────────┐
                    │ EventBridge  │─────────▶ │ SQS queue    │──failures─▶│   DLQ    │
                    │ default bus  │           └──────┬───────┘            └──────────┘
                    └──────────────┘                  │ long poll
                                                      ▼
                                               ┌──────────────┐
                                               │ worker (Go)  │
                                               │ 1 pod, k3s   │
                                               └──────────────┘
```

**Endpoints**

| Method | Path                            | What it does                                                 |
| ------ | ------------------------------- | ------------------------------------------------------------ |
| `POST` | `/chargers`                     | Create a charger, save to DynamoDB, publish `ChargerCreated` |
| `GET`  | `/chargers/{id}`                | Fetch one charger                                            |
| `GET`  | `/chargers?lat=&lng=&radiusKm=` | Chargers within range, nearest first (default 10 km)         |
| `GET`  | `/chargers`                     | All chargers                                                 |
| `GET`  | `/healthz`                      | Health check for Kubernetes probes                           |

**Layout**

```
cmd/api/          HTTP server entrypoint (falls back to in-memory if TABLE_NAME unset)
cmd/worker/       SQS consumer
internal/charger/ domain model + Store/Publisher interfaces (no AWS imports)
internal/api/     handlers + tests (tested with in-memory fakes)
internal/memory/  in-memory Store/Publisher
internal/cloud/   DynamoDB + EventBridge implementations
k8s/              namespace, api Deployment+Service, worker Deployment
scripts/          AWS setup/teardown, k3s image loader
.github/          CI: vet, race tests, docker build
```

---
