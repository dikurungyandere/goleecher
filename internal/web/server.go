package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/dikurungyandere/goleecher/internal/config"
	"github.com/dikurungyandere/goleecher/internal/store"
)

//go:embed static/index.html
var staticFS embed.FS

// Server is the web dashboard HTTP server.
type Server struct {
	cfg     *config.Config
	st      *store.Store
	startAt time.Time
	mux     *http.ServeMux
}

// NewServer creates a new web Server.
func NewServer(cfg *config.Config, st *store.Store) *Server {
	s := &Server{
		cfg:     cfg,
		st:      st,
		startAt: time.Now(),
		mux:     http.NewServeMux(),
	}
	s.mux.HandleFunc("/", s.handleIndex)
	s.mux.HandleFunc("/api/jobs", s.handleJobs)
	s.mux.HandleFunc("/api/stats", s.handleStats)
	return s
}

// Start starts listening. Blocks until the server fails.
func (s *Server) Start() error {
	return http.ListenAndServe(fmt.Sprintf(":%s", s.cfg.Port), s.mux)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data) //nolint:errcheck // best-effort write to HTTP response
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	jobs := s.st.All()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(jobs); err != nil {
		http.Error(w, "encoding error", http.StatusInternalServerError)
	}
}

type statsResponse struct {
	ActiveJobs int64  `json:"active_jobs"`
	TotalJobs  int64  `json:"total_jobs"`
	TotalBytes int64  `json:"total_bytes"`
	Uptime     string `json:"uptime"`
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	all := s.st.All()
	active := s.st.Active()
	uptime := time.Since(s.startAt).Truncate(time.Second).String()

	resp := statsResponse{
		ActiveJobs: int64(len(active)),
		TotalJobs:  int64(len(all)),
		TotalBytes: s.st.TotalBytes(),
		Uptime:     uptime,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "encoding error", http.StatusInternalServerError)
	}
}
