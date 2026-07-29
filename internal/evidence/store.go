package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"fluxagent/api/v1alpha1"
	"fluxagent/internal/canonicaldigest"
	"fluxagent/internal/domain"
)

const (
	LocalFilesystemStoreName = "local-filesystem"
	normalizedSnapshotClass  = "normalized-snapshot"
	normalizedSnapshotV1     = "fluxagent-normalized-evidence-snapshot-v1"
)

type SnapshotStore interface {
	Name() string
	StoreNormalizedSnapshot(ctx context.Context, snapshot NormalizedSnapshot) (v1alpha1.PayloadRef, error)
}

type NormalizedSnapshot struct {
	Policy      v1alpha1.EvidenceRetentionPolicy `json:"policy,omitempty"`
	Observation domain.Observation               `json:"observation"`
	CreatedAt   metav1.Time                      `json:"createdAt"`
	ExpiresAt   *metav1.Time                     `json:"expiresAt,omitempty"`
}

type LocalFilesystemStore struct {
	Root string
}

func (s LocalFilesystemStore) Name() string {
	return LocalFilesystemStoreName
}

func (s LocalFilesystemStore) StoreNormalizedSnapshot(ctx context.Context, snapshot NormalizedSnapshot) (v1alpha1.PayloadRef, error) {
	root := strings.TrimSpace(s.Root)
	if root == "" {
		return v1alpha1.PayloadRef{}, fmt.Errorf("local filesystem evidence store root is empty")
	}
	select {
	case <-ctx.Done():
		return v1alpha1.PayloadRef{}, ctx.Err()
	default:
	}

	payload := map[string]any{
		"schemaVersion": normalizedSnapshotV1,
		"snapshot":      snapshot,
	}
	digest := canonicaldigest.String(canonicaldigest.RCAJSONV1, payload)
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return v1alpha1.PayloadRef{}, err
	}

	filename := strings.TrimPrefix(digest, "sha256:") + ".json"
	path := filepath.Join(root, filename)
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(path)
	if cleanPath != filepath.Join(cleanRoot, filename) {
		return v1alpha1.PayloadRef{}, fmt.Errorf("invalid evidence snapshot path")
	}
	if err := os.MkdirAll(cleanRoot, 0o700); err != nil {
		return v1alpha1.PayloadRef{}, err
	}
	if err := os.WriteFile(cleanPath, data, 0o600); err != nil {
		return v1alpha1.PayloadRef{}, err
	}

	return v1alpha1.PayloadRef{
		Scheme:         "file",
		Digest:         digest,
		Encrypted:      false,
		ExpiresAt:      snapshot.ExpiresAt,
		RetentionClass: normalizedSnapshotClass,
	}, nil
}

func NormalizedSnapshotExpiry(now time.Time, policy v1alpha1.EvidenceRetentionPolicy) *metav1.Time {
	if policy.Retention.Duration <= 0 {
		return nil
	}
	expiresAt := metav1.NewTime(now.Add(policy.Retention.Duration))
	return &expiresAt
}
