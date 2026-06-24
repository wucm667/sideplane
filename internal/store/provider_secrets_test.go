package store

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMemoryProviderSecretsRoundTrip(t *testing.T) {
	assertProviderSecretsRoundTrip(t, NewMemoryNodeStore())
}

func TestSQLiteProviderSecretsRoundTrip(t *testing.T) {
	ctx := context.Background()
	nodeStore, err := OpenSQLiteNodeStore(ctx, filepath.Join(t.TempDir(), "sideplane.db"))
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	defer nodeStore.Close()

	assertProviderSecretsRoundTrip(t, nodeStore)
}

func assertProviderSecretsRoundTrip(t *testing.T, nodeStore Store) {
	t.Helper()
	ctx := context.Background()
	envName := "OPENAI_API_KEY"
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)

	if got, ok, err := nodeStore.GetProviderSecret(ctx, envName); err != nil {
		t.Fatalf("get missing provider secret: %v", err)
	} else if ok || got != nil {
		t.Fatalf("missing provider secret = (%#v, %t), want nil/false", got, ok)
	}
	if ok, err := nodeStore.HasProviderSecret(ctx, envName); err != nil {
		t.Fatalf("has missing provider secret: %v", err)
	} else if ok {
		t.Fatal("missing provider secret reported present")
	}

	first := []byte("opaque-ciphertext-v1")
	if err := nodeStore.SetProviderSecret(ctx, envName, first, now); err != nil {
		t.Fatalf("set provider secret: %v", err)
	}
	got, ok, err := nodeStore.GetProviderSecret(ctx, envName)
	if err != nil {
		t.Fatalf("get provider secret: %v", err)
	}
	if !ok || !bytes.Equal(got, first) {
		t.Fatalf("provider secret = (%q, %t), want first ciphertext", got, ok)
	}
	got[0] = 'X'
	gotAgain, ok, err := nodeStore.GetProviderSecret(ctx, envName)
	if err != nil {
		t.Fatalf("get provider secret again: %v", err)
	}
	if !ok || !bytes.Equal(gotAgain, first) {
		t.Fatalf("provider secret was mutated through returned slice: %q", gotAgain)
	}
	if ok, err := nodeStore.HasProviderSecret(ctx, envName); err != nil {
		t.Fatalf("has provider secret: %v", err)
	} else if !ok {
		t.Fatal("provider secret reported missing")
	}

	second := []byte("opaque-ciphertext-v2")
	if err := nodeStore.SetProviderSecret(ctx, envName, second, now.Add(time.Minute)); err != nil {
		t.Fatalf("overwrite provider secret: %v", err)
	}
	got, ok, err = nodeStore.GetProviderSecret(ctx, envName)
	if err != nil {
		t.Fatalf("get overwritten provider secret: %v", err)
	}
	if !ok || !bytes.Equal(got, second) {
		t.Fatalf("overwritten provider secret = (%q, %t), want second ciphertext", got, ok)
	}

	if err := nodeStore.DeleteProviderSecret(ctx, envName); err != nil {
		t.Fatalf("delete provider secret: %v", err)
	}
	if got, ok, err := nodeStore.GetProviderSecret(ctx, envName); err != nil {
		t.Fatalf("get deleted provider secret: %v", err)
	} else if ok || got != nil {
		t.Fatalf("deleted provider secret = (%#v, %t), want nil/false", got, ok)
	}
}
