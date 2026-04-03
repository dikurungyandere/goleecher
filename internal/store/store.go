package store

import (
	"sort"
	"sync"
	"time"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusDownloading Status = "downloading"
	StatusUploading  Status = "uploading"
	StatusDone       Status = "done"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
)

type Job struct {
	ID         string    `json:"id"`
	UserID     int64     `json:"user_id"`
	ChatID     int64     `json:"chat_id"`
	URL        string    `json:"url"`
	Filename   string    `json:"filename"`
	Status     Status    `json:"status"`
	Progress   float64   `json:"progress"`
	Speed      int64     `json:"speed"`
	TotalBytes int64     `json:"total_bytes"`
	DoneBytes  int64     `json:"done_bytes"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	Cancel     func()    `json:"-"`
}

type Store struct {
	mu         sync.RWMutex
	jobs       map[string]*Job
	adminID    int64
	allowedIDs map[int64]struct{}
	totalBytes int64
}

func New(adminID int64, allowedIDs []int64) *Store {
	m := make(map[int64]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		m[id] = struct{}{}
	}
	return &Store{
		jobs:       make(map[string]*Job),
		adminID:    adminID,
		allowedIDs: m,
	}
}

func (s *Store) IsAllowed(userID int64) bool {
	if s.adminID != 0 && userID == s.adminID {
		return true
	}
	if len(s.allowedIDs) == 0 {
		return true // open to all if no restrictions set
	}
	_, ok := s.allowedIDs[userID]
	return ok
}

func (s *Store) IsAdmin(userID int64) bool {
	return s.adminID != 0 && userID == s.adminID
}

func (s *Store) Add(job *Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.jobs[job.ID] = job
}

func (s *Store) Get(id string) (*Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	j, ok := s.jobs[id]
	return j, ok
}

func (s *Store) Update(id string, fn func(*Job)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return false
	}
	fn(j)
	return true
}

func (s *Store) All() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, j)
	}
	return out
}

func (s *Store) Active() []*Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Job
	for _, j := range s.jobs {
		if j.Status == StatusDownloading || j.Status == StatusUploading || j.Status == StatusPending {
			out = append(out, j)
		}
	}
	return out
}

// ActiveSorted returns active jobs sorted by creation time (oldest first).
func (s *Store) ActiveSorted() []*Job {
	jobs := s.Active()
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
	})
	return jobs
}

func (s *Store) CancelAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, j := range s.jobs {
		if j.Cancel != nil &&
			(j.Status == StatusDownloading || j.Status == StatusUploading || j.Status == StatusPending) {
			j.Cancel()
		}
	}
}

func (s *Store) AddTotalBytes(n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.totalBytes += n
}

func (s *Store) TotalBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalBytes
}
