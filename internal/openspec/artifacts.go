package openspec

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chatinfra/specd/internal/apierr"
)

func (r *Repository) ArtifactContent(kind, id, relPath string) (ArtifactContentResponse, error) {
	absolute, cleanRel, err := r.resolveEntryArtifact(kind, id, relPath)
	if err != nil {
		return ArtifactContentResponse{}, err
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ArtifactContentResponse{}, apierr.New(http.StatusNotFound, "artifact_not_found", "artifact was not found")
		}
		return ArtifactContentResponse{}, apierr.Wrap(http.StatusInternalServerError, "artifact_read_failed", "failed to read artifact", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return ArtifactContentResponse{}, apierr.Wrap(http.StatusInternalServerError, "artifact_read_failed", "failed to inspect artifact", err)
	}
	token := revisionToken(data, info)
	node := artifactNodeForFile(cleanRel, absolute, artifactKind(cleanRel), info, true)
	return ArtifactContentResponse{Artifact: node, Content: string(data), RevisionToken: token}, nil
}

func (r *Repository) UpdateArtifact(req ArtifactUpdateRequest) (ArtifactUpdateResponse, error) {
	absolute, cleanRel, err := r.resolveEntryArtifact(req.EntryKind, req.EntryID, req.Path)
	if err != nil {
		return ArtifactUpdateResponse{}, err
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ArtifactUpdateResponse{}, apierr.New(http.StatusNotFound, "artifact_not_found", "artifact update is restricted to existing OpenSpec artifacts")
		}
		return ArtifactUpdateResponse{}, apierr.Wrap(http.StatusInternalServerError, "artifact_read_failed", "failed to read current artifact", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return ArtifactUpdateResponse{}, apierr.Wrap(http.StatusInternalServerError, "artifact_read_failed", "failed to inspect current artifact", err)
	}
	current := revisionToken(data, info)
	if strings.TrimSpace(req.ExpectedRevisionToken) == "" || req.ExpectedRevisionToken != current {
		return ArtifactUpdateResponse{}, apierr.WithDetails(apierr.New(http.StatusConflict, "stale_revision", "artifact revision token is stale"), map[string]any{"currentRevisionToken": current})
	}
	if err := os.WriteFile(absolute, []byte(req.Content), info.Mode().Perm()); err != nil {
		return ArtifactUpdateResponse{}, apierr.Wrap(http.StatusInternalServerError, "artifact_write_failed", "failed to write artifact", err)
	}
	updated, err := os.ReadFile(absolute)
	if err != nil {
		return ArtifactUpdateResponse{}, apierr.Wrap(http.StatusInternalServerError, "artifact_read_failed", "failed to read updated artifact", err)
	}
	updatedInfo, err := os.Stat(absolute)
	if err != nil {
		return ArtifactUpdateResponse{}, apierr.Wrap(http.StatusInternalServerError, "artifact_read_failed", "failed to inspect updated artifact", err)
	}
	token := revisionToken(updated, updatedInfo)
	node := artifactNodeForFile(cleanRel, absolute, artifactKind(cleanRel), updatedInfo, true)
	return ArtifactUpdateResponse{Artifact: node, RevisionToken: token}, nil
}

func (r *Repository) resolveEntryArtifact(kind, id, relPath string) (string, string, error) {
	dir, err := r.entryDir(kind, id)
	if err != nil {
		return "", "", err
	}
	clean := filepath.Clean(strings.TrimSpace(relPath))
	clean = filepath.ToSlash(clean)
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") || filepath.IsAbs(clean) {
		return "", "", apierr.New(http.StatusBadRequest, "path_traversal", "artifact path must stay inside allowed OpenSpec artifact locations")
	}
	entryRel, err := filepath.Rel(r.Root, dir)
	if err != nil {
		return "", "", apierr.Wrap(http.StatusInternalServerError, "artifact_path_failed", "failed to resolve entry path", err)
	}
	entryRel = filepath.ToSlash(entryRel)
	if clean == entryRel || strings.HasPrefix(clean, entryRel+"/") {
		clean = strings.TrimPrefix(clean, entryRel+"/")
	}
	if !allowedEntryArtifact(clean) {
		return "", "", apierr.New(http.StatusBadRequest, "artifact_path_not_allowed", "artifact path is not an editable OpenSpec artifact")
	}
	absolute := filepath.Join(dir, filepath.FromSlash(clean))
	resolved, err := filepath.Abs(absolute)
	if err != nil {
		return "", "", apierr.Wrap(http.StatusInternalServerError, "artifact_path_failed", "failed to resolve artifact path", err)
	}
	entryAbs, _ := filepath.Abs(dir)
	if resolved != entryAbs && !strings.HasPrefix(resolved, entryAbs+string(os.PathSeparator)) {
		return "", "", apierr.New(http.StatusBadRequest, "path_traversal", "artifact path escapes the OpenSpec entry")
	}
	repoRel, err := filepath.Rel(r.Root, resolved)
	if err != nil {
		return "", "", apierr.Wrap(http.StatusInternalServerError, "artifact_path_failed", "failed to build artifact path", err)
	}
	return resolved, filepath.ToSlash(repoRel), nil
}

func allowedEntryArtifact(clean string) bool {
	switch clean {
	case "proposal.md", "design.md", "tasks.md", "README.md":
		return true
	}
	return strings.HasPrefix(clean, "specs/") && strings.HasSuffix(clean, "/spec.md") && !strings.Contains(clean, "//")
}

func (r *Repository) artifactTree(kind, id string) []ArtifactNode {
	dir, err := r.entryDir(kind, id)
	if err != nil {
		return nil
	}
	entryRel, _ := filepath.Rel(r.Root, dir)
	entryRel = filepath.ToSlash(entryRel)
	paths := []string{"proposal.md", "design.md", "tasks.md", "README.md"}
	_ = filepath.WalkDir(filepath.Join(dir, "specs"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Base(path) == "spec.md" {
			rel, relErr := filepath.Rel(dir, path)
			if relErr == nil {
				paths = append(paths, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(paths[4:])
	nodes := make([]ArtifactNode, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		abs := filepath.Join(dir, filepath.FromSlash(p))
		info, err := os.Stat(abs)
		repoRel := entryRel + "/" + p
		if err != nil {
			nodes = append(nodes, ArtifactNode{Path: repoRel, Name: filepath.Base(p), Kind: artifactKind(p), Exists: false, Editable: false, Directory: false})
			continue
		}
		nodes = append(nodes, artifactNodeForFile(repoRel, abs, artifactKind(p), info, !info.IsDir()))
	}
	return nodes
}

func artifactNodeForFile(repoRel, absolute, kind string, info fs.FileInfo, editable bool) ArtifactNode {
	modified := info.ModTime().UTC()
	size := info.Size()
	node := ArtifactNode{Path: filepath.ToSlash(repoRel), Name: filepath.Base(repoRel), Kind: kind, Exists: true, Editable: editable && !info.IsDir(), Directory: info.IsDir(), SizeBytes: &size, ModifiedAt: &modified}
	if !info.IsDir() {
		if data, err := os.ReadFile(absolute); err == nil {
			token := revisionToken(data, info)
			node.RevisionToken = &token
		}
	}
	return node
}

func artifactKind(rel string) string {
	base := filepath.Base(rel)
	switch base {
	case "proposal.md":
		return "proposal"
	case "design.md":
		return "design"
	case "tasks.md":
		return "tasks"
	case "README.md":
		return "readme"
	case "spec.md":
		return "spec"
	default:
		return "other"
	}
}

func (r *Repository) CanonicalSpecs(includeContent bool) (CanonicalSpecListResponse, error) {
	base := filepath.Join(r.Root, "openspec", "specs")
	var specs []CanonicalSpecSummary
	_ = filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Base(path) != "spec.md" {
			return nil
		}
		summary, err := r.canonicalSummary(path, includeContent)
		if err == nil {
			specs = append(specs, summary)
		}
		return nil
	})
	sort.SliceStable(specs, func(i, j int) bool { return specs[i].Capability < specs[j].Capability })
	return CanonicalSpecListResponse{Specs: specs}, nil
}

func (r *Repository) CanonicalSpec(capability string) (CanonicalSpecDetailResponse, error) {
	if !validName(capability) {
		return CanonicalSpecDetailResponse{}, apierr.New(http.StatusBadRequest, "invalid_capability", "capability name is invalid")
	}
	path := filepath.Join(r.Root, "openspec", "specs", capability, "spec.md")
	summary, err := r.canonicalSummary(path, true)
	if err != nil {
		return CanonicalSpecDetailResponse{}, err
	}
	content := ""
	if summary.Content != nil {
		content = *summary.Content
	}
	summary.Content = nil
	return CanonicalSpecDetailResponse{Spec: summary, Content: content}, nil
}

func (r *Repository) canonicalSummary(path string, includeContent bool) (CanonicalSpecSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return CanonicalSpecSummary{}, apierr.New(http.StatusNotFound, "canonical_spec_not_found", "canonical spec was not found")
		}
		return CanonicalSpecSummary{}, apierr.Wrap(http.StatusInternalServerError, "canonical_spec_read_failed", "failed to read canonical spec", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return CanonicalSpecSummary{}, apierr.Wrap(http.StatusInternalServerError, "canonical_spec_read_failed", "failed to inspect canonical spec", err)
	}
	rel, _ := filepath.Rel(r.Root, path)
	capability := filepath.Base(filepath.Dir(path))
	var content *string
	if includeContent {
		value := string(data)
		content = &value
	}
	return CanonicalSpecSummary{Capability: capability, Path: filepath.ToSlash(rel), SizeBytes: info.Size(), ModifiedAt: info.ModTime().UTC(), Content: content}, nil
}

func recentActivity(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	value := t.UTC().Format(time.RFC3339)
	return &value
}
