package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"

	"github.com/dikurungyandere/goleecher/internal/store"
)

// ProgressFunc is called periodically with bytes done, total, and speed.
type ProgressFunc func(done, total, speed int64)

// Manager wraps the store and provides job lifecycle management.
type Manager struct {
	st *store.Store
}

func New(st *store.Store) *Manager {
	return &Manager{st: st}
}

// NewJob creates a new job, registers it in the store, and returns it.
func (m *Manager) NewJob(ctx context.Context, userID, chatID int64, url string) (*store.Job, context.Context, context.CancelFunc) {
	jobCtx, cancel := context.WithCancel(ctx)
	job := &store.Job{
		ID:        newID(),
		UserID:    userID,
		ChatID:    chatID,
		URL:       url,
		Status:    store.StatusPending,
		CreatedAt: time.Now(),
		Cancel:    cancel,
	}
	m.st.Add(job)
	return job, jobCtx, cancel
}

// SetFilename sets the filename for a job (called when the filename is known before download completes).
func (m *Manager) SetFilename(id, filename string) {
	m.st.Update(id, func(j *store.Job) {
		if filename != "" {
			j.Filename = filename
		}
	})
}

// SetDownloading marks a job as downloading.
func (m *Manager) SetDownloading(id string) {
	m.st.Update(id, func(j *store.Job) {
		j.Status = store.StatusDownloading
	})
}

// SetUploading marks a job as uploading.
func (m *Manager) SetUploading(id, filename string) {
	m.st.Update(id, func(j *store.Job) {
		j.Status = store.StatusUploading
		if filename != "" {
			j.Filename = filename
		}
		j.Progress = 0
	})
}

// SetDone marks a job as done.
func (m *Manager) SetDone(id string, totalBytes int64) {
	m.st.Update(id, func(j *store.Job) {
		j.Status = store.StatusDone
		j.Progress = 100
		j.Speed = 0
	})
	m.st.AddTotalBytes(totalBytes)
}

// SetFailed marks a job as failed.
func (m *Manager) SetFailed(id string, err error) {
	m.st.Update(id, func(j *store.Job) {
		j.Status = store.StatusFailed
		j.Error = err.Error()
	})
}

// SetCancelled marks a job as cancelled.
func (m *Manager) SetCancelled(id string) {
	m.st.Update(id, func(j *store.Job) {
		j.Status = store.StatusCancelled
	})
}

// ProgressUpdater returns a ProgressFunc that updates the job in the store.
func (m *Manager) ProgressUpdater(id string) ProgressFunc {
	return func(done, total, speed int64) {
		m.st.Update(id, func(j *store.Job) {
			j.DoneBytes = done
			j.TotalBytes = total
			j.Speed = speed
			if total > 0 {
				j.Progress = float64(done) / float64(total) * 100
			}
		})
	}
}

// CancelJob cancels a single job by ID. Returns error if not found or not cancellable.
func (m *Manager) CancelJob(id string, requesterID int64) error {
	j, ok := m.st.Get(id)
	if !ok {
		return fmt.Errorf("job %s not found", id)
	}
	if j.UserID != requesterID && !m.st.IsAdmin(requesterID) {
		return fmt.Errorf("not authorized to cancel job %s", id)
	}
	if j.Status != store.StatusDownloading &&
		j.Status != store.StatusUploading &&
		j.Status != store.StatusPending {
		return fmt.Errorf("job %s is not active (status: %s)", id, j.Status)
	}
	if j.Cancel != nil {
		j.Cancel()
	}
	return nil
}

func newID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// fallback: use time-based hex (should not normally happen)
		return fmt.Sprintf("%08x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// JobFilePath returns the path where a job's download should be stored.
func JobFilePath(tempDir, jobID, filename string) string {
	return filepath.Join(tempDir, jobID, filename)
}
