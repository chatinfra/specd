package openspec

import "time"

const (
	KindBacklog  = "backlog"
	KindActive   = "active"
	KindArchived = "archived"

	SuperdirAssociationActiveChange   = "active-change"
	SuperdirAssociationBacklog        = "backlog"
	SuperdirAssociationArchivedChange = "archived-change"
	SuperdirAssociationInfrastructure = "infrastructure"
	SuperdirAssociationUnmatched      = "unmatched"

	SuperdirAssociationSourceExplicit = "explicit"
	SuperdirAssociationSourceLegacy   = "legacy"
	SuperdirAssociationSourceInferred = "inferred"
)

type TaskProgress struct {
	Completed int `json:"completed"`
	Total     int `json:"total"`
}

type ArtifactPresence struct {
	Kind    string `json:"kind"`
	Present bool   `json:"present"`
	Count   *int   `json:"count,omitempty"`
}

type EntrySummary struct {
	Kind             string             `json:"kind"`
	ID               string             `json:"id"`
	Title            string             `json:"title"`
	SchemaName       *string            `json:"schemaName,omitempty"`
	Status           *string            `json:"status,omitempty"`
	TaskProgress     *TaskProgress      `json:"taskProgress,omitempty"`
	ArtifactPresence []ArtifactPresence `json:"artifactPresence"`
	LastModified     time.Time          `json:"lastModified"`
}

type EntryListResponse struct {
	Entries []EntrySummary `json:"entries"`
}

type EntryDetailResponse struct {
	Entry               EntrySummary      `json:"entry"`
	Artifacts           []ArtifactNode    `json:"artifacts"`
	AssociatedSuperdirs []SuperdirSummary `json:"associatedSuperdirs"`
}

type ArtifactNode struct {
	Path          string         `json:"path"`
	Name          string         `json:"name,omitempty"`
	Kind          string         `json:"kind"`
	Exists        bool           `json:"exists"`
	Editable      bool           `json:"editable"`
	Directory     bool           `json:"directory"`
	SizeBytes     *int64         `json:"sizeBytes,omitempty"`
	ModifiedAt    *time.Time     `json:"modifiedAt,omitempty"`
	RevisionToken *string        `json:"revisionToken,omitempty"`
	Children      []ArtifactNode `json:"children,omitempty"`
}

type ArtifactContentResponse struct {
	Artifact      ArtifactNode `json:"artifact"`
	Content       string       `json:"content"`
	RevisionToken string       `json:"revisionToken"`
}

type ArtifactUpdateRequest struct {
	EntryKind             string `json:"entryKind"`
	EntryID               string `json:"entryId"`
	Path                  string `json:"path"`
	Content               string `json:"content"`
	ExpectedRevisionToken string `json:"expectedRevisionToken"`
}

type ArtifactUpdateResponse struct {
	Artifact      ArtifactNode `json:"artifact"`
	RevisionToken string       `json:"revisionToken"`
}

type CreateEntryRequest struct {
	Name  string  `json:"name"`
	Title *string `json:"title,omitempty"`
}

type CanonicalSpecSummary struct {
	Capability string    `json:"capability"`
	Path       string    `json:"path"`
	SizeBytes  int64     `json:"sizeBytes"`
	ModifiedAt time.Time `json:"modifiedAt"`
	Content    *string   `json:"content,omitempty"`
}

type CanonicalSpecListResponse struct {
	Specs []CanonicalSpecSummary `json:"specs"`
}

type CanonicalSpecDetailResponse struct {
	Spec    CanonicalSpecSummary `json:"spec"`
	Content string               `json:"content"`
}

type SuperdirListResponse struct {
	Superdirs []SuperdirSummary `json:"superdirs"`
}

type SuperdirSummary struct {
	ID                  string           `json:"id"`
	DirectoryName       string           `json:"directoryName"`
	DirectoryPath       string           `json:"directoryPath"`
	ChangeName          string           `json:"changeName"`
	TargetName          string           `json:"targetName"`
	AssociationKind     string           `json:"associationKind"`
	AssociationSource   string           `json:"associationSource"`
	AssociatedEntryKind *string          `json:"associatedEntryKind,omitempty"`
	AssociatedEntryName *string          `json:"associatedEntryName,omitempty"`
	Active              bool             `json:"active"`
	RecentActivity      *string          `json:"recentActivity,omitempty"`
	TaskProgress        *TaskProgress    `json:"taskProgress,omitempty"`
	ServiceBindings     []ServiceBinding `json:"serviceBindings,omitempty"`
	ServerLocal         bool             `json:"serverLocal"`
}

type ServiceBinding struct {
	Service      string `json:"service"`
	URL          string `json:"url"`
	LoopbackOnly bool   `json:"loopbackOnly"`
	ServerLocal  bool   `json:"serverLocal"`
}

type ValidateResponse struct {
	OK     bool     `json:"ok"`
	Output string   `json:"output"`
	Errors []string `json:"errors,omitempty"`
}
