# go-task-queue

A lightweight **async task queue with worker pool** built in Go. Submit jobs via HTTP, process them in the background, track their status in real time — with automatic retries and exponential backoff.

## Features

- ✅ Worker pool with configurable concurrency
- ✅ Job type registry — register any handler function
- ✅ Automatic retry with **exponential backoff** on failure
- ✅ Job status tracking: `pending → processing → done/failed`
- ✅ REST API: enqueue, check status, list all jobs
- ✅ Thread-safe job store with `sync.RWMutex`
- ✅ Graceful shutdown via `context.Context`

## Quick Start

```bash
git clone https://github.com/yourusername/go-task-queue
cd go-task-queue
go mod tidy
go run main.go
```

## API

### Enqueue a Job
```http
POST /jobs
Content-Type: application/json

{
  "type": "send_email",
  "payload": { "to": "user@example.com", "subject": "Welcome!" },
  "max_retry": 3
}
```
```json
{ "id": "abc-123", "status": "pending", "type": "send_email", ... }
```

### Check Job Status
```http
GET /jobs/{id}
```
```json
{ "id": "abc-123", "status": "done", "result": { "message_id": "..." }, ... }
```

### List All Jobs
```http
GET /jobs
```

## Job Lifecycle

```
POST /jobs → pending → processing → done ✅
                              ↓ (on failure)
                           retrying (with backoff) → failed ❌
```

## Retry Strategy

Retries use **exponential backoff**: 1s → 2s → 4s → give up.

```go
delay := time.Duration(1<<job.Retries) * time.Second
```

## Register Custom Handlers

```go
pool.RegisterHandler("generate_pdf", func(payload map[string]any) (map[string]any, error) {
    // your logic here
    return map[string]any{"url": "/output/report.pdf"}, nil
})
```

## Built-in Job Types

| Type           | Description                          |
|----------------|--------------------------------------|
| `send_email`   | Simulates email sending (20% failure)|
| `resize_image` | Simulates image resize (always ok)   |

## Tech Stack

- **Go** 1.22+ (uses `r.PathValue` for routing)
- `github.com/google/uuid`
- stdlib only (`net/http`, `sync`, `context`)

## License

MIT
