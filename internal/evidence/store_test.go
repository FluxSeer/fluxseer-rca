package evidence

import (
	"context"
	"strings"
	"testing"
)

func TestDurableStoreNamesAreDeclared(t *testing.T) {
	names := DurableStoreNames()
	want := []string{S3StoreName, GCSStoreName, AzureBlobStoreName, PVCStoreName}
	if len(names) != len(want) {
		t.Fatalf("expected durable store names %#v, got %#v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("expected durable store %q at index %d, got %q", want[i], i, names[i])
		}
	}
}

func TestUnsupportedStoreFailsClosed(t *testing.T) {
	store := UnsupportedStore{StoreName: S3StoreName}
	if store.Name() != S3StoreName {
		t.Fatalf("expected store name %q, got %q", S3StoreName, store.Name())
	}
	ref, err := store.StoreNormalizedSnapshot(context.Background(), NormalizedSnapshot{})
	if err == nil {
		t.Fatal("expected unsupported store error")
	}
	if ref.Scheme != "" || ref.Digest != "" {
		t.Fatalf("expected empty payload ref on unsupported store, got %#v", ref)
	}
	if !strings.Contains(err.Error(), S3StoreName) {
		t.Fatalf("expected store name in error, got %v", err)
	}
}
