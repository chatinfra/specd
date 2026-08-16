package openspec

import (
	"bufio"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func (r *Repository) ListSuperdirs(superRoot string) (SuperdirListResponse, error) {
	if strings.TrimSpace(superRoot) == "" {
		superRoot = "/data/super"
	}
	dirs, err := os.ReadDir(superRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return SuperdirListResponse{Superdirs: []SuperdirSummary{}}, nil
		}
		return SuperdirListResponse{Superdirs: []SuperdirSummary{}}, nil
	}
	items := make([]SuperdirSummary, 0, len(dirs))
	for _, d := range dirs {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		path := filepath.Join(superRoot, d.Name())
		env := readSafeEnv(filepath.Join(path, ".env"))
		infrastructure := isCanonicalInfrastructureClone(d.Name())
		targetName, associationSource := superdirTarget(env, d.Name())
		associationKind, associatedEntryKind, associatedEntryName := r.classifySuperdirTarget(targetName)
		if infrastructure {
			targetName = d.Name()
			associationSource = SuperdirAssociationSourceInferred
			associationKind = SuperdirAssociationInfrastructure
			associatedEntryKind = nil
			associatedEntryName = nil
		}
		mtime, _ := newestMTime(path)
		var progress *TaskProgress
		for _, candidate := range []string{filepath.Join(path, "tasks.md"), filepath.Join(path, "openspec", "changes", targetName, "tasks.md")} {
			progress = readTaskProgress(candidate)
			if progress != nil {
				break
			}
		}
		serviceBindings := safeServiceBindings(env)
		if infrastructure {
			serviceBindings = nil
		}
		items = append(items, SuperdirSummary{
			ID:                  d.Name(),
			DirectoryName:       d.Name(),
			DirectoryPath:       path,
			ChangeName:          targetName,
			TargetName:          targetName,
			AssociationKind:     associationKind,
			AssociationSource:   associationSource,
			AssociatedEntryKind: associatedEntryKind,
			AssociatedEntryName: associatedEntryName,
			Active:              fileExists(filepath.Join(path, ".env")),
			RecentActivity:      recentActivity(mtime),
			TaskProgress:        progress,
			ServiceBindings:     serviceBindings,
			ServerLocal:         true,
		})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].DirectoryName < items[j].DirectoryName })
	return SuperdirListResponse{Superdirs: items}, nil
}

func FilterSuperdirs(items []SuperdirSummary, changeName string) []SuperdirSummary {
	return FilterSuperdirsForEntry(items, KindActive, changeName)
}

func FilterSuperdirsForEntry(items []SuperdirSummary, entryKind, entryName string) []SuperdirSummary {
	if entryKind == "" || entryName == "" {
		return nil
	}
	var out []SuperdirSummary
	for _, item := range items {
		if item.AssociationKind == SuperdirAssociationInfrastructure {
			continue
		}
		if item.AssociatedEntryKind != nil && item.AssociatedEntryName != nil && *item.AssociatedEntryKind == entryKind && *item.AssociatedEntryName == entryName {
			out = append(out, item)
		}
	}
	return out
}

func isCanonicalInfrastructureClone(dirName string) bool {
	switch dirName {
	case "merge-train", "archive-train", "super-master":
		return true
	default:
		return false
	}
}

func superdirTarget(env map[string]string, dirName string) (string, string) {
	if value := strings.TrimSpace(env["CHANGE_NAME"]); value != "" {
		return value, SuperdirAssociationSourceExplicit
	}
	if value := strings.TrimSpace(env["SUPER_CHANGE"]); value != "" {
		return value, SuperdirAssociationSourceLegacy
	}
	return inferChangeName(dirName), SuperdirAssociationSourceInferred
}

func (r *Repository) classifySuperdirTarget(targetName string) (string, *string, *string) {
	targetName = strings.TrimSpace(targetName)
	if targetName == "" {
		return SuperdirAssociationUnmatched, nil, nil
	}
	if isDir(filepath.Join(r.Root, "openspec", "changes", targetName)) && targetName != "archive" {
		kind := KindActive
		name := targetName
		return SuperdirAssociationActiveChange, &kind, &name
	}
	if r.backlogEntryExists(targetName) {
		kind := KindBacklog
		name := targetName
		return SuperdirAssociationBacklog, &kind, &name
	}
	if archiveName := r.archivedEntryName(targetName); archiveName != "" {
		kind := KindArchived
		name := archiveName
		return SuperdirAssociationArchivedChange, &kind, &name
	}
	return SuperdirAssociationUnmatched, nil, nil
}

func (r *Repository) backlogEntryExists(targetName string) bool {
	root := filepath.Join(r.Root, "openspec", "backlog")
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if d.Name() == "archive" {
			return filepath.SkipDir
		}
		if d.Name() == targetName && fileExists(filepath.Join(path, "proposal.md")) {
			found = true
			return filepath.SkipDir
		}
		return nil
	})
	return found
}

func (r *Repository) archivedEntryName(targetName string) string {
	root := filepath.Join(r.Root, "openspec", "changes", "archive")
	dirs, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	var matches []string
	for _, d := range dirs {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		if d.Name() == targetName || strings.HasSuffix(d.Name(), "-"+targetName) {
			matches = append(matches, d.Name())
		}
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func readSafeEnv(path string) map[string]string {
	file, err := os.Open(path)
	if err != nil {
		return map[string]string{}
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if secretKey(key) {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		values[key] = value
	}
	return values
}

func safeServiceBindings(env map[string]string) []ServiceBinding {
	var bindings []ServiceBinding
	for key, value := range env {
		upper := strings.ToUpper(key)
		if !strings.HasSuffix(upper, "URL") || value == "" || secretKey(key) {
			continue
		}
		parsed, err := url.Parse(value)
		loopback := false
		if err == nil {
			host := parsed.Hostname()
			loopback = host == "127.0.0.1" || host == "localhost" || host == "::1"
		}
		bindings = append(bindings, ServiceBinding{Service: strings.TrimSuffix(strings.ToLower(key), "_url"), URL: value, LoopbackOnly: loopback, ServerLocal: true})
	}
	sort.SliceStable(bindings, func(i, j int) bool { return bindings[i].Service < bindings[j].Service })
	return bindings
}

func secretKey(key string) bool {
	upper := strings.ToUpper(key)
	secretParts := []string{"TOKEN", "SECRET", "PASSWORD", "KEY", "CREDENTIAL", "AUTH"}
	for _, part := range secretParts {
		if strings.Contains(upper, part) {
			return true
		}
	}
	return false
}

func inferChangeName(name string) string {
	parts := strings.Split(name, "-")
	if len(parts) > 1 {
		last := parts[len(parts)-1]
		allDigits := last != ""
		for _, r := range last {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return strings.Join(parts[:len(parts)-1], "-")
		}
	}
	return name
}
