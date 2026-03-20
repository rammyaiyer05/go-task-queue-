package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
)

// --- Job Status ---

type JobStatus string

const (
	StatusPending    JobStatus = "pending"
	StatusProcessing JobStatus = "processing"
	StatusDone       JobStatus = "done"
	StatusFailed     JobStatus = "failed"
)

// --- Job ---

type Job struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Payload   map[string]any    `json:"payload"`
	Status    JobStatus         `json:"status"`
	Retries   int               `json:"retries"`
	MaxRetry  int               `json:"max_retry"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Error     string            `json:"error,omitempty"`
	Result    map[string]any    `json:"result,omitempty"`
}

// --- Queue ---

type Queue struct {
	jobs    map[string]*Job
	channel chan *Job
	mu      sync.RWMutex
}

func NewQueue(buffer int) *Queue {
	return &Queue{
		jobs:    make(map[string]*Job),
		channel: make(chan *Job, buffer),
	}
}

func (q *Queue) Enqueue(jobType string, payload map[string]any, maxRetry int) *Job {
	job := &Job{
		ID:        uuid.New().String(),
		Type:      jobType,
		Payload:   payload,
		Status:    StatusPending,
		MaxRetry:  maxRetry,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	q.mu.Lock()
	q.jobs[job.ID] = job
	q.mu.Unlock()
	q.channel <- job
	return job
}

func (q *Queue) GetJob(id string) (*Job, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	job, ok := q.jobs[id]
	return job, ok
}

func (q *Queue) ListJobs() []*Job {
	q.mu.RLock()
	defer q.mu.RUnlock()
	jobs := make([]*Job, 0, len(q.jobs))
	for _, j := range q.jobs {
		jobs = append(jobs, j)
	}
	return jobs
}

func (q *Queue) updateJob(id string, fn func(*Job)) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if job, ok := q.jobs[id]; ok {
		fn(job)
		job.UpdatedAt = time.Now()
	}
}

// --- Worker Pool ---

type WorkerPool struct {
	queue      *Queue
	numWorkers int
	handlers   map[string]func(map[string]any) (map[string]any, error)
}

func NewWorkerPool(q *Queue, numWorkers int) *WorkerPool {
	return &WorkerPool{
		queue:      q,
		numWorkers: numWorkers,
		handlers:   make(map[string]func(map[string]any) (map[string]any, error)),
	}
}

func (wp *WorkerPool) RegisterHandler(jobType string, handler func(map[string]any) (map[string]any, error)) {
	wp.handlers[jobType] = handler
}

func (wp *WorkerPool) Start(ctx context.Context) {
	for i := 0; i < wp.numWorkers; i++ {
		go wp.worker(ctx, i+1)
	}
}

func (wp *WorkerPool) worker(ctx context.Context, id int) {
	log.Printf("[Worker %d] started", id)
	for {
		select {
		case <-ctx.Done():
			log.Printf("[Worker %d] stopped", id)
			return
		case job := <-wp.queue.channel:
			wp.process(job, id)
		}
	}
}

func (wp *WorkerPool) process(job *Job, workerID int) {
	log.Printf("[Worker %d] processing job %s (type: %s)", workerID, job.ID[:8], job.Type)

	wp.queue.updateJob(job.ID, func(j *Job) { j.Status = StatusProcessing })

	handler, ok := wp.handlers[job.Type]
	if !ok {
		wp.queue.updateJob(job.ID, func(j *Job) {
			j.Status = StatusFailed
			j.Error = fmt.Sprintf("no handler registered for job type: %s", job.Type)
		})
		return
	}

	result, err := handler(job.Payload)
	if err != nil {
		if job.Retries < job.MaxRetry {
			delay := time.Duration(1<<job.Retries) * time.Second
			log.Printf("[Worker %d] job %s failed, retrying in %v (attempt %d/%d)",
				workerID, job.ID[:8], delay, job.Retries+1, job.MaxRetry)
			wp.queue.updateJob(job.ID, func(j *Job) {
				j.Status = StatusPending
				j.Retries++
			})
			time.AfterFunc(delay, func() { wp.queue.channel <- job })
		} else {
			wp.queue.updateJob(job.ID, func(j *Job) {
				j.Status = StatusFailed
				j.Error = err.Error()
			})
		}
		return
	}

	wp.queue.updateJob(job.ID, func(j *Job) {
		j.Status = StatusDone
		j.Result = result
	})
	log.Printf("[Worker %d] job %s completed", workerID, job.ID[:8])
}

// --- HTTP Server ---

type Server struct {
	queue *Queue
	pool  *WorkerPool
}

func (s *Server) enqueueHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type     string         `json:"type"`
		Payload  map[string]any `json:"payload"`
		MaxRetry int            `json:"max_retry"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.Type == "" {
		http.Error(w, `{"error":"job type is required"}`, http.StatusBadRequest)
		return
	}
	if req.MaxRetry == 0 {
		req.MaxRetry = 3
	}
	job := s.queue.Enqueue(req.Type, req.Payload, req.MaxRetry)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(job)
}

func (s *Server) statusHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	job, ok := s.queue.GetJob(id)
	if !ok {
		http.Error(w, `{"error":"job not found"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(job)
}

func (s *Server) listHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.queue.ListJobs())
}

// --- Example Handlers ---

func emailHandler(payload map[string]any) (map[string]any, error) {
	time.Sleep(500 * time.Millisecond) // simulate work
	if rand.Float32() < 0.2 {
		return nil, fmt.Errorf("SMTP connection failed (simulated)")
	}
	return map[string]any{"sent_to": payload["to"], "message_id": uuid.New().String()}, nil
}

func resizeImageHandler(payload map[string]any) (map[string]any, error) {
	time.Sleep(1 * time.Second) // simulate work
	return map[string]any{"output": "/tmp/resized_" + uuid.New().String() + ".jpg", "width": 800}, nil
}

func main() {
	q := NewQueue(100)
	pool := NewWorkerPool(q, 4)
	pool.RegisterHandler("send_email", emailHandler)
	pool.RegisterHandler("resize_image", resizeImageHandler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)

	srv := &Server{queue: q, pool: pool}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /jobs", srv.enqueueHandler)
	mux.HandleFunc("GET /jobs", srv.listHandler)
	mux.HandleFunc("GET /jobs/{id}", srv.statusHandler)

	fmt.Println("🚀 Task queue running at http://localhost:8080")
	fmt.Println("   Workers: 4 | Buffer: 100")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
