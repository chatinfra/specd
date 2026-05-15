package openspec

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRepositoryInventoryArtifactsCanonicalAndTraversal(t *testing.T) {
	repoRoot := repoFixture(t)
	repo := NewRepository(repoRoot, fakeOpenSpec(t), time.Second)
	entries, err := repo.List(KindActive)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].ID != "alpha-change" || entries[0].TaskProgress == nil || entries[0].TaskProgress.Total != 2 {
		t.Fatalf("entries = %#v", entries)
	}
	detail, err := repo.Detail(KindActive, "alpha-change")
	if err != nil {
		t.Fatalf("Detail() error = %v", err)
	}
	if len(detail.Artifacts) == 0 {
		t.Fatalf("detail artifacts empty")
	}
	content, err := repo.ArtifactContent(KindActive, "alpha-change", "openspec/changes/alpha-change/tasks.md")
	if err != nil || content.RevisionToken == "" {
		t.Fatalf("ArtifactContent() token=%q err=%v", content.RevisionToken, err)
	}
	if _, err := repo.ArtifactContent(KindActive, "alpha-change", "../project.md"); err == nil {
		t.Fatal("path traversal unexpectedly succeeded")
	}
	if _, err := repo.UpdateArtifact(ArtifactUpdateRequest{EntryKind: KindActive, EntryID: "alpha-change", Path: "openspec/changes/alpha-change/missing.md", Content: "x", ExpectedRevisionToken: content.RevisionToken}); err == nil {
		t.Fatal("arbitrary artifact creation unexpectedly succeeded")
	}
	updated, err := repo.UpdateArtifact(ArtifactUpdateRequest{EntryKind: KindActive, EntryID: "alpha-change", Path: "openspec/changes/alpha-change/tasks.md", Content: "- [x] one\n- [x] two\n", ExpectedRevisionToken: content.RevisionToken})
	if err != nil || updated.RevisionToken == content.RevisionToken {
		t.Fatalf("UpdateArtifact() token=%q err=%v", updated.RevisionToken, err)
	}
	canonical, err := repo.CanonicalSpecs(true)
	if err != nil || len(canonical.Specs) != 1 || canonical.Specs[0].Content == nil {
		t.Fatalf("CanonicalSpecs() = %#v err=%v", canonical, err)
	}
}

func TestRepositoryCreateRejectsDuplicates(t *testing.T) {
	repoRoot := repoFixture(t)
	repo := NewRepository(repoRoot, fakeOpenSpec(t), time.Second)
	if _, err := repo.Create(KindActive, CreateEntryRequest{Name: "new-change"}); err != nil {
		t.Fatalf("Create(new-change) error = %v", err)
	}
	if _, err := repo.Create(KindActive, CreateEntryRequest{Name: "new-change"}); err == nil {
		t.Fatal("duplicate create unexpectedly succeeded")
	}
	if _, err := repo.Create(KindBacklog, CreateEntryRequest{Name: "bad_name"}); err == nil {
		t.Fatal("invalid name unexpectedly succeeded")
	}
}

func TestRepositoryListSuperdirsClassifiesAssociationMetadataSafely(t *testing.T) {
	repoRoot := repoFixture(t)
	mustWrite(t, filepath.Join(repoRoot, "openspec", "backlog", "backlog-change", "proposal.md"), "proposal\n")
	mustWrite(t, filepath.Join(repoRoot, "openspec", "changes", "archive", "2026-05-12-archived-change", "proposal.md"), "proposal\n")
	superRoot := filepath.Join(tempDir(t), "superdirs")
	mustWrite(t, filepath.Join(superRoot, "wrong-name-1234567890", ".env"), "CHANGE_NAME=alpha-change\nADMIN_TOKEN=secret\nOPENCODE_URL=http://127.0.0.1:2721\n")
	mustWrite(t, filepath.Join(superRoot, "backlog-change-1234567890", ".env"), "SUPER_CHANGE=backlog-change\n")
	mustWrite(t, filepath.Join(superRoot, "archived-change-1234567890", ".env"), "SUPER_NAME=archived-change-1234567890\n")
	mustWrite(t, filepath.Join(superRoot, "typo-change-1234567890", ".env"), "SUPER_NAME=typo-change-1234567890\n")

	repo := NewRepository(repoRoot, fakeOpenSpec(t), time.Second)
	resp, err := repo.ListSuperdirs(superRoot)
	if err != nil {
		t.Fatalf("ListSuperdirs() error = %v", err)
	}
	byDir := map[string]SuperdirSummary{}
	for _, item := range resp.Superdirs {
		byDir[item.DirectoryName] = item
	}

	explicit := byDir["wrong-name-1234567890"]
	if explicit.TargetName != "alpha-change" || explicit.AssociationKind != SuperdirAssociationActiveChange || explicit.AssociationSource != SuperdirAssociationSourceExplicit {
		t.Fatalf("explicit association = %#v", explicit)
	}
	if explicit.AssociatedEntryKind == nil || *explicit.AssociatedEntryKind != KindActive || explicit.AssociatedEntryName == nil || *explicit.AssociatedEntryName != "alpha-change" {
		t.Fatalf("explicit associated entry = %#v", explicit)
	}
	if len(explicit.ServiceBindings) != 1 || explicit.ServiceBindings[0].Service != "opencode" {
		t.Fatalf("safe bindings = %#v", explicit.ServiceBindings)
	}

	backlog := byDir["backlog-change-1234567890"]
	if backlog.AssociationKind != SuperdirAssociationBacklog || backlog.AssociationSource != SuperdirAssociationSourceLegacy {
		t.Fatalf("backlog association = %#v", backlog)
	}
	archived := byDir["archived-change-1234567890"]
	if archived.AssociationKind != SuperdirAssociationArchivedChange || archived.AssociatedEntryName == nil || *archived.AssociatedEntryName != "2026-05-12-archived-change" {
		t.Fatalf("archived association = %#v", archived)
	}
	unmatched := byDir["typo-change-1234567890"]
	if unmatched.AssociationKind != SuperdirAssociationUnmatched || unmatched.AssociatedEntryKind != nil || unmatched.AssociatedEntryName != nil {
		t.Fatalf("unmatched association = %#v", unmatched)
	}
}

func repoFixture(t *testing.T) string {
	t.Helper()
	root := tempDir(t)
	repoRoot := filepath.Join(root, "repo")
	mustWrite(t, filepath.Join(repoRoot, "openspec", "project.md"), "# project\n")
	mustWrite(t, filepath.Join(repoRoot, "openspec", "changes", "alpha-change", "proposal.md"), "proposal\n")
	mustWrite(t, filepath.Join(repoRoot, "openspec", "changes", "alpha-change", "tasks.md"), "- [x] one\n- [ ] two\n")
	mustWrite(t, filepath.Join(repoRoot, "openspec", "changes", "alpha-change", "specs", "alpha", "spec.md"), "spec\n")
	mustWrite(t, filepath.Join(repoRoot, "openspec", "specs", "canonical-alpha", "spec.md"), "canonical\n")
	return repoRoot
}

func fakeOpenSpec(t *testing.T) string {
	t.Helper()
	path := filepath.Join(tempDir(t), "openspec")
	mustWrite(t, path, "#!/usr/bin/env bash\nprintf '{}'\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod fake openspec: %v", err)
	}
	return path
}

func tempDir(t *testing.T) string {
	t.Helper()
	base := os.Getenv("SUPER_TMP_DIR")
	if base == "" {
		base = filepath.Join("..", "..", "..", "tmp", "specd-tests")
	}
	_ = os.MkdirAll(base, 0o755)
	dir, err := os.MkdirTemp(base, "openspec-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
