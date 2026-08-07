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

	"github.com/FluxSeer/fluxseer-rca/api/v1alpha1"
	"github.com/FluxSeer/fluxseer-rca/internal/canonicaldigest"
	"github.com/FluxSeer/fluxseer-rca/internal/domain"
)

const (
	LocalFilesystemStoreName = "local-filesystem"
	S3StoreName              = "s3"
	GCSStoreName             = "gcs"
	AzureBlobStoreName       = "azure-blob"
	PVCStoreName             = "pvc"
	normalizedSnapshotClass  = "normalized-snapshot"
	normalizedSnapshotV1     = "fluxseer-rca-normalized-evidence-snapshot-v1"
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

type UnsupportedStore struct {
	StoreName string
}

func SupportedStoreNames() []string {
	return []string{
		LocalFilesystemStoreName,
		S3StoreName,
		GCSStoreName,
		AzureBlobStoreName,
		PVCStoreName,
	}
}

func DurableStoreNames() []string {
	return []string{
		S3StoreName,
		GCSStoreName,
		AzureBlobStoreName,
		PVCStoreName,
	}
}

func (s LocalFilesystemStore) Name() string {
	return LocalFilesystemStoreName
}

func (s UnsupportedStore) Name() string {
	return strings.TrimSpace(s.StoreName)
}

func (s UnsupportedStore) StoreNormalizedSnapshot(context.Context, NormalizedSnapshot) (v1alpha1.PayloadRef, error) {
	name := strings.TrimSpace(s.StoreName)
	if name == "" {
		name = "unknown"
	}
	return v1alpha1.PayloadRef{}, fmt.Errorf("evidence snapshot store %q is not implemented in this build", name)
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
