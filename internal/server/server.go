package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chatinfra/specd/internal/apierr"
	"github.com/chatinfra/specd/internal/job"
	"github.com/chatinfra/specd/internal/openspec"
	"github.com/chatinfra/specd/internal/workflow"
)

type Server struct {
	Config Config
	Repo   *openspec.Repository
	Jobs   *job.Store
	Runner *workflow.Runner
	mux    *http.ServeMux
}

func New(config Config, repo *openspec.Repository, jobs *job.Store, runner *workflow.Runner) *Server {
	server := &Server{Config: config, Repo: repo, Jobs: jobs, Runner: runner, mux: http.NewServeMux()}
	server.routes()
	return server
}

func (s *Server) Handler() http.Handler {
	return s.auth(s.mux)
}

func Serve(ctx context.Context, config Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	repo, jobs, err := config.NewDependencies()
	if err != nil {
		return err
	}
	runner := workflow.NewRunner(jobs, repo, config.EnableAgentWorkflows)
	httpServer := &http.Server{Addr: config.Address(), Handler: New(config, repo, jobs, runner).Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	err = httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) routes() {
	s.mux.HandleFunc("/health", s.method(http.MethodGet, s.handleHealth))
	s.mux.HandleFunc("/server", s.method(http.MethodGet, s.handleMetadata))
	s.mux.HandleFunc("/entries", s.method(http.MethodGet, s.handleListEntries))
	s.mux.HandleFunc("/entries/changes", s.method(http.MethodPost, s.handleCreateChange))
	s.mux.HandleFunc("/entries/backlog", s.method(http.MethodPost, s.handleCreateBacklog))
	s.mux.HandleFunc("/entries/", s.method(http.MethodGet, s.handleEntryDetail))
	s.mux.HandleFunc("/artifacts/content", s.method(http.MethodGet, s.handleArtifactContent))
	s.mux.HandleFunc("/artifacts", s.method(http.MethodPut, s.handleArtifactUpdate))
	s.mux.HandleFunc("/canonical-specs", s.method(http.MethodGet, s.handleCanonicalSpecs))
	s.mux.HandleFunc("/canonical-specs/", s.method(http.MethodGet, s.handleCanonicalSpec))
	s.mux.HandleFunc("/openspec/status", s.method(http.MethodPost, s.handleOpenSpecStatus))
	s.mux.HandleFunc("/openspec/instructions", s.method(http.MethodPost, s.handleOpenSpecInstructions))
	s.mux.HandleFunc("/openspec/validate", s.method(http.MethodPost, s.handleOpenSpecValidate))
	s.mux.HandleFunc("/openspec/sync", s.method(http.MethodPost, s.handleOpenSpecSync))
	s.mux.HandleFunc("/openspec/archive", s.method(http.MethodPost, s.handleOpenSpecArchive))
	s.mux.HandleFunc("/superdirs", s.method(http.MethodGet, s.handleSuperdirs))
	s.mux.HandleFunc("/superdirs/setup", s.method(http.MethodPost, s.handleSuperdirSetup))
	s.mux.HandleFunc("/jobs", s.method(http.MethodGet, s.handleJobs))
	s.mux.HandleFunc("/jobs/", s.handleJobSubroutes)
	s.mux.HandleFunc("/opsx/propose", s.method(http.MethodPost, s.handleOpsx("propose")))
	s.mux.HandleFunc("/opsx/continue", s.method(http.MethodPost, s.handleOpsx("continue")))
	s.mux.HandleFunc("/opsx/ff", s.method(http.MethodPost, s.handleOpsx("ff")))
	s.mux.HandleFunc("/opsx/apply", s.method(http.MethodPost, s.handleOpsx("apply")))
	s.mux.HandleFunc("/opsx/verify", s.method(http.MethodPost, s.handleOpsx("verify")))
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")) != s.Config.BearerToken {
			writeError(w, apierr.New(http.StatusUnauthorized, "unauthorized", "missing, malformed, or invalid bearer token"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) method(method string, handler func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			writeError(w, apierr.New(http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed"))
			return
		}
		if err := handler(w, r); err != nil {
			writeError(w, err)
		}
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "serverTime": time.Now().UTC(), "repositoryReady": true, "openspecReady": true, "jobStoreReady": true})
}

func (s *Server) handleMetadata(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, http.StatusOK, s.Config.Metadata())
}

func (s *Server) handleListEntries(w http.ResponseWriter, r *http.Request) error {
	kind := r.URL.Query().Get("kind")
	entries, err := s.Repo.List(kind)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, openspec.EntryListResponse{Entries: entries})
}

func (s *Server) handleEntryDetail(w http.ResponseWriter, r *http.Request) error {
	kind, id, err := parseEntryPath(r.URL.Path)
	if err != nil {
		return err
	}
	detail, err := s.Repo.Detail(kind, id)
	if err != nil {
		return err
	}
	superdirs, _ := s.Repo.ListSuperdirs(s.Config.SuperRoot)
	detail.AssociatedSuperdirs = openspec.FilterSuperdirsForEntry(superdirs.Superdirs, kind, id)
	return writeJSON(w, http.StatusOK, detail)
}

func (s *Server) handleCreateChange(w http.ResponseWriter, r *http.Request) error {
	var req openspec.CreateEntryRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	detail, err := s.Repo.Create(openspec.KindActive, req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, detail)
}

func (s *Server) handleCreateBacklog(w http.ResponseWriter, r *http.Request) error {
	var req openspec.CreateEntryRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	detail, err := s.Repo.Create(openspec.KindBacklog, req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, detail)
}

func (s *Server) handleArtifactContent(w http.ResponseWriter, r *http.Request) error {
	resp, err := s.Repo.ArtifactContent(r.URL.Query().Get("entryKind"), r.URL.Query().Get("entryId"), r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleArtifactUpdate(w http.ResponseWriter, r *http.Request) error {
	var req openspec.ArtifactUpdateRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	resp, err := s.Repo.UpdateArtifact(req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCanonicalSpecs(w http.ResponseWriter, r *http.Request) error {
	includeContent := r.URL.Query().Get("includeContent") == "true"
	resp, err := s.Repo.CanonicalSpecs(includeContent)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleCanonicalSpec(w http.ResponseWriter, r *http.Request) error {
	capability := strings.TrimPrefix(r.URL.Path, "/canonical-specs/")
	resp, err := s.Repo.CanonicalSpec(capability)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleOpenSpecStatus(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ChangeName string `json:"changeName"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	resp, err := s.Repo.Status(r.Context(), req.ChangeName)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleOpenSpecInstructions(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ChangeName string `json:"changeName"`
		Action     string `json:"action"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	resp, err := s.Repo.Instructions(r.Context(), req.Action, req.ChangeName)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleOpenSpecValidate(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		TargetType string `json:"targetType"`
		Name       string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	resp, err := s.Repo.Validate(r.Context(), req.TargetType, req.Name)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleOpenSpecSync(w http.ResponseWriter, r *http.Request) error {
	return s.submitOpenSpecJob(w, r, "sync")
}

func (s *Server) handleOpenSpecArchive(w http.ResponseWriter, r *http.Request) error {
	return s.submitOpenSpecJob(w, r, "archive")
}

func (s *Server) submitOpenSpecJob(w http.ResponseWriter, r *http.Request, operation string) error {
	var req struct {
		ChangeName string `json:"changeName"`
	}
	if err := readJSON(r, &req); err != nil {
		return err
	}
	resource, err := s.Runner.SubmitOpenSpecJob(r.Context(), operation, req.ChangeName)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, resource)
}

func (s *Server) handleSuperdirs(w http.ResponseWriter, r *http.Request) error {
	resp, err := s.Repo.ListSuperdirs(s.Config.SuperRoot)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSuperdirSetup(w http.ResponseWriter, r *http.Request) error {
	var req workflow.SuperdirSetupRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	resource, err := s.Runner.SubmitSuperdirSetup(r.Context(), req)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, resource)
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) error {
	jobs, err := s.Jobs.List()
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, job.ListResponse{Jobs: jobs})
}

func (s *Server) handleJobSubroutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/jobs/")
	parts := strings.Split(path, "/")
	if len(parts) == 1 && r.Method == http.MethodGet {
		s.method(http.MethodGet, s.handleJobDetail)(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" {
		s.method(http.MethodPost, s.handleJobCancel)(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "events" {
		s.method(http.MethodGet, s.handleJobEvents)(w, r)
		return
	}
	if len(parts) == 4 && parts[1] == "prompts" && parts[2] != "" {
		if parts[3] == "reply" {
			s.method(http.MethodPost, s.handlePromptReply)(w, r)
			return
		}
		if parts[3] == "reject" {
			s.method(http.MethodPost, s.handlePromptReject)(w, r)
			return
		}
	}
	writeError(w, apierr.New(http.StatusNotFound, "not_found", "route was not found"))
}

func (s *Server) handleJobDetail(w http.ResponseWriter, r *http.Request) error {
	resource, err := s.Jobs.Get(jobIDFromPath(r.URL.Path))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, resource)
}

func (s *Server) handleJobCancel(w http.ResponseWriter, r *http.Request) error {
	resource, err := s.Jobs.Cancel(jobIDFromPath(r.URL.Path))
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, resource)
}

func (s *Server) handleJobEvents(w http.ResponseWriter, r *http.Request) error {
	jobID := jobIDFromPath(r.URL.Path)
	after, _ := strconv.ParseInt(r.URL.Query().Get("afterSequence"), 10, 64)
	events, err := s.Jobs.EventsAfter(jobID, after)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	for _, event := range events {
		if err := writeSSE(w, event); err != nil {
			return err
		}
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	if r.URL.Query().Get("follow") == "false" {
		return nil
	}
	ch, unsubscribe := s.Jobs.Subscribe(jobID)
	defer unsubscribe()
	for {
		select {
		case event := <-ch:
			if err := writeSSE(w, event); err != nil {
				return err
			}
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		case <-r.Context().Done():
			return nil
		}
	}
}

func (s *Server) handlePromptReply(w http.ResponseWriter, r *http.Request) error {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/jobs/"), "/")
	var req job.PromptReplyRequest
	if err := readJSON(r, &req); err != nil {
		return err
	}
	resource, err := s.Jobs.ReplyPrompt(parts[0], parts[2], req.Answers)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, resource)
}

func (s *Server) handlePromptReject(w http.ResponseWriter, r *http.Request) error {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/jobs/"), "/")
	resource, err := s.Jobs.RejectPrompt(parts[0], parts[2])
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, resource)
}

func (s *Server) handleOpsx(operation string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		var req workflow.OpsxRequest
		if err := readJSON(r, &req); err != nil {
			return err
		}
		resource, err := s.Runner.SubmitAgent(r.Context(), operation, req)
		if err != nil {
			return err
		}
		return writeJSON(w, http.StatusAccepted, resource)
	}
}

func readJSON(r *http.Request, target any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return apierr.Wrap(http.StatusBadRequest, "invalid_json", "request body must be valid JSON", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	typed := apierr.From(err)
	status := typed.Status
	if status == 0 {
		status = http.StatusInternalServerError
	}
	_ = writeJSON(w, status, typed)
}

func writeSSE(w http.ResponseWriter, event job.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, data)
	return err
}

func parseEntryPath(path string) (string, string, error) {
	parts := strings.Split(strings.TrimPrefix(path, "/entries/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", apierr.New(http.StatusBadRequest, "invalid_entry_path", "entry path must be /entries/{entryKind}/{entryId}")
	}
	return parts[0], parts[1], nil
}

func jobIDFromPath(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/jobs/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}
