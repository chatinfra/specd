package openspec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/chatinfra/specd/internal/apierr"
)

type Repository struct {
	Root        string
	OpenSpecBin string
	Timeout     time.Duration
}

func NewRepository(root, openspecBin string, timeout time.Duration) *Repository {
	return &Repository{Root: filepath.Clean(root), OpenSpecBin: openspecBin, Timeout: timeout}
}

func ValidateLayout(root string) error {
	if strings.TrimSpace(root) == "" {
		return apierr.New(http.StatusBadRequest, "missing_repo_root", "repository root is required")
	}
	info, err := os.Stat(root)
	if err != nil {
		return apierr.Wrap(http.StatusBadRequest, "invalid_repo_root", "repository root does not exist", err)
	}
	if !info.IsDir() {
		return apierr.New(http.StatusBadRequest, "invalid_repo_root", "repository root is not a directory")
	}
	openspecDir := filepath.Join(root, "openspec")
	info, err = os.Stat(openspecDir)
	if err != nil || !info.IsDir() {
		return apierr.New(http.StatusBadRequest, "invalid_openspec_layout", "repository root must contain an openspec directory")
	}
	if _, err := os.Stat(filepath.Join(root, "openspec", "project.md")); err != nil {
		if _, cfgErr := os.Stat(filepath.Join(root, "openspec", "config.yaml")); cfgErr != nil {
			return apierr.New(http.StatusBadRequest, "invalid_openspec_layout", "openspec directory must contain project.md or config.yaml")
		}
	}
	if info.Mode().Perm()&0200 == 0 {
		return apierr.New(http.StatusBadRequest, "repo_not_writable", "openspec directory is not writable for mutating operations")
	}
	return nil
}

func (r *Repository) List(kind string) ([]EntrySummary, error) {
	if kind == "" || kind == "all" {
		var all []EntrySummary
		for _, k := range []string{KindBacklog, KindActive, KindArchived} {
			entries, err := r.List(k)
			if err != nil {
				return nil, err
			}
			all = append(all, entries...)
		}
		return all, nil
	}
	base, err := r.baseDir(kind)
	if err != nil {
		return nil, err
	}
	dirs, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []EntrySummary{}, nil
		}
		return nil, apierr.Wrap(http.StatusInternalServerError, "entry_list_failed", "failed to list OpenSpec entries", err)
	}
	entries := make([]EntrySummary, 0, len(dirs))
	for _, d := range dirs {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		if kind == KindActive && d.Name() == "archive" {
			continue
		}
		summary, err := r.summary(kind, d.Name())
		if err != nil {
			return nil, err
		}
		entries = append(entries, summary)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	return entries, nil
}

func (r *Repository) Detail(kind, id string) (EntryDetailResponse, error) {
	summary, err := r.summary(kind, id)
	if err != nil {
		return EntryDetailResponse{}, err
	}
	return EntryDetailResponse{Entry: summary, Artifacts: r.artifactTree(kind, id), AssociatedSuperdirs: []SuperdirSummary{}}, nil
}

func (r *Repository) Create(kind string, req CreateEntryRequest) (EntryDetailResponse, error) {
	name := strings.TrimSpace(req.Name)
	if !validName(name) {
		return EntryDetailResponse{}, apierr.New(http.StatusBadRequest, "invalid_entry_name", "entry name must be kebab-case lowercase letters, numbers, and hyphens")
	}
	if kind != KindActive && kind != KindBacklog {
		return EntryDetailResponse{}, apierr.New(http.StatusBadRequest, "invalid_entry_kind", "only active changes and backlog entries can be created")
	}
	base, err := r.baseDir(kind)
	if err != nil {
		return EntryDetailResponse{}, err
	}
	dir := filepath.Join(base, name)
	if _, err := os.Stat(dir); err == nil {
		return EntryDetailResponse{}, apierr.New(http.StatusConflict, "entry_exists", "OpenSpec entry already exists")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return EntryDetailResponse{}, apierr.Wrap(http.StatusInternalServerError, "entry_create_failed", "failed to inspect target entry", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return EntryDetailResponse{}, apierr.Wrap(http.StatusInternalServerError, "entry_create_failed", "failed to create entry directory", err)
	}
	title := name
	if req.Title != nil && strings.TrimSpace(*req.Title) != "" {
		title = strings.TrimSpace(*req.Title)
	}
	if err := os.WriteFile(filepath.Join(dir, "proposal.md"), []byte("## Why\n\n"+title+"\n\n## What Changes\n\n- TBD\n"), 0o644); err != nil {
		return EntryDetailResponse{}, apierr.Wrap(http.StatusInternalServerError, "entry_create_failed", "failed to write proposal scaffold", err)
	}
	return r.Detail(kind, name)
}

func (r *Repository) baseDir(kind string) (string, error) {
	switch kind {
	case KindActive:
		return filepath.Join(r.Root, "openspec", "changes"), nil
	case KindBacklog:
		return filepath.Join(r.Root, "openspec", "backlog"), nil
	case KindArchived:
		return filepath.Join(r.Root, "openspec", "changes", "archive"), nil
	default:
		return "", apierr.New(http.StatusBadRequest, "invalid_entry_kind", "entry kind must be backlog, active, archived, or all")
	}
}

func (r *Repository) entryDir(kind, id string) (string, error) {
	if strings.TrimSpace(id) == "" || strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return "", apierr.New(http.StatusBadRequest, "invalid_entry_id", "entry id is invalid")
	}
	base, err := r.baseDir(kind)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, id)
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", apierr.New(http.StatusNotFound, "entry_not_found", "OpenSpec entry was not found")
		}
		return "", apierr.Wrap(http.StatusInternalServerError, "entry_read_failed", "failed to inspect OpenSpec entry", err)
	}
	if !info.IsDir() {
		return "", apierr.New(http.StatusNotFound, "entry_not_found", "OpenSpec entry was not found")
	}
	return dir, nil
}

func (r *Repository) summary(kind, id string) (EntrySummary, error) {
	dir, err := r.entryDir(kind, id)
	if err != nil {
		return EntrySummary{}, err
	}
	progress := readTaskProgress(filepath.Join(dir, "tasks.md"))
	status := kind
	if kind == KindActive && progress != nil && progress.Total > 0 && progress.Completed == progress.Total {
		status = "complete"
	}
	lastModified, err := newestMTime(dir)
	if err != nil {
		return EntrySummary{}, err
	}
	schemaName := readSchemaName(filepath.Join(dir, ".openspec.yaml"))
	return EntrySummary{
		Kind:             kind,
		ID:               id,
		Title:            id,
		SchemaName:       schemaName,
		Status:           &status,
		TaskProgress:     progress,
		ArtifactPresence: r.artifactPresence(dir),
		LastModified:     lastModified,
	}, nil
}

func (r *Repository) artifactPresence(dir string) []ArtifactPresence {
	countSpecs := 0
	_ = filepath.WalkDir(filepath.Join(dir, "specs"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Base(path) == "spec.md" {
			countSpecs++
		}
		return nil
	})
	specCount := countSpecs
	return []ArtifactPresence{
		{Kind: "proposal", Present: fileExists(filepath.Join(dir, "proposal.md"))},
		{Kind: "design", Present: fileExists(filepath.Join(dir, "design.md"))},
		{Kind: "tasks", Present: fileExists(filepath.Join(dir, "tasks.md"))},
		{Kind: "readme", Present: fileExists(filepath.Join(dir, "README.md"))},
		{Kind: "spec", Present: countSpecs > 0, Count: &specCount},
	}
}

func readTaskProgress(path string) *TaskProgress {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	completed := 0
	total := 0
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- [x]") || strings.HasPrefix(trimmed, "- [X]") {
			completed++
			total++
		} else if strings.HasPrefix(trimmed, "- [ ]") {
			total++
		}
	}
	if total == 0 {
		return nil
	}
	return &TaskProgress{Completed: completed, Total: total}
}

func readSchemaName(path string) *string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "schema:") || strings.HasPrefix(trimmed, "schemaName:") {
			_, value, _ := strings.Cut(trimmed, ":")
			value = strings.Trim(strings.TrimSpace(value), "\"'")
			if value != "" {
				return &value
			}
		}
	}
	return nil
}

func newestMTime(root string) (time.Time, error) {
	newest := time.Time{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if err != nil {
		return time.Time{}, apierr.Wrap(http.StatusInternalServerError, "entry_read_failed", "failed to inspect OpenSpec entry metadata", err)
	}
	if newest.IsZero() {
		newest = time.Now().UTC()
	}
	return newest.UTC(), nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func revisionToken(data []byte, info fs.FileInfo) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%s:size:%d:mtime:%d", hex.EncodeToString(h[:]), info.Size(), info.ModTime().UnixNano())
}

var entryNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

func validName(name string) bool {
	return entryNamePattern.MatchString(name) && !strings.Contains(name, "--") && !strings.HasSuffix(name, "-")
}
