package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAndRead(t *testing.T) {
	b := &Local{Dir: filepath.Join(t.TempDir(), ".solaria", "state")}
	got, err := b.Read("dev")
	if err != nil || got != nil {
		t.Fatalf("Read of a new environment = (%v, %v), want (nil, nil)", got, err)
	}

	s := New("dev")
	s.Agents["Agent/a"] = &Entry{ID: "Agent/a", ResolvedDigest: "sha256:1"}
	if err := b.Write(s); err != nil {
		t.Fatal(err)
	}
	if s.Serial != 1 {
		t.Errorf("serial after first write = %d, want 1", s.Serial)
	}
	got, err = b.Read("dev")
	if err != nil {
		t.Fatal(err)
	}
	if got.Lineage != s.Lineage || got.Serial != 1 || got.Agents["Agent/a"].ResolvedDigest != "sha256:1" {
		t.Errorf("read back %+v", got)
	}
	if err := b.Write(got); err != nil || got.Serial != 2 {
		t.Errorf("second write: serial %d, err %v; want 2, nil", got.Serial, err)
	}
	if _, err := os.Stat(filepath.Join(b.Dir, "..", ".gitignore")); err != nil {
		t.Errorf("no .solaria/.gitignore: %v", err)
	}
	envs, err := b.Environments()
	if err != nil || len(envs) != 1 || envs[0] != "dev" {
		t.Errorf("Environments = %v, %v; want [dev]", envs, err)
	}
}

func TestWriteRefusesStaleSerial(t *testing.T) {
	b := &Local{Dir: t.TempDir()}
	if err := b.Write(New("dev")); err != nil {
		t.Fatal(err)
	}
	first, _ := b.Read("dev")
	second, _ := b.Read("dev")
	if err := b.Write(first); err != nil {
		t.Fatal(err)
	}
	if err := b.Write(second); !errors.Is(err, ErrStale) {
		t.Errorf("write of a stale copy: err = %v, want ErrStale", err)
	}
}

func TestWriteRefusesOtherLineage(t *testing.T) {
	b := &Local{Dir: t.TempDir()}
	if err := b.Write(New("dev")); err != nil {
		t.Fatal(err)
	}
	other := New("dev")
	other.Serial = 1
	if err := b.Write(other); !errors.Is(err, ErrStale) {
		t.Errorf("write with another lineage: err = %v, want ErrStale", err)
	}
}

func TestWriteRefusesRemovedState(t *testing.T) {
	b := &Local{Dir: t.TempDir()}
	s := New("dev")
	if err := b.Write(s); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(b.statePath("dev")); err != nil {
		t.Fatal(err)
	}
	if err := b.Write(s); !errors.Is(err, ErrStale) {
		t.Errorf("write after the file was removed: err = %v, want ErrStale", err)
	}
}

func TestLock(t *testing.T) {
	b := &Local{Dir: t.TempDir()}
	info := LockInfo{Operation: "apply", Who: "renan", Created: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
	nonce, err := b.Lock("dev", info)
	if err != nil {
		t.Fatal(err)
	}

	_, err = b.Lock("dev", info)
	var locked *LockedError
	if !errors.As(err, &locked) || locked.Holder.Nonce != nonce || locked.Holder.Operation != "apply" {
		t.Fatalf("second Lock: err = %v, want a LockedError naming the holder", err)
	}

	if err := b.Unlock("dev", "wrong"); err == nil {
		t.Error("Unlock with the wrong nonce succeeded")
	}
	if err := b.ForceUnlock("dev", nonce); err != nil {
		t.Fatal(err)
	}
	if err := b.Unlock("dev", nonce); err == nil {
		t.Error("Unlock of an unlocked environment succeeded")
	}
	if _, err := b.Lock("dev", info); err != nil {
		t.Errorf("Lock after unlock: %v", err)
	}
}

func TestReadRejectsUnknownFormat(t *testing.T) {
	b := &Local{Dir: t.TempDir()}
	if err := os.WriteFile(b.statePath("dev"), []byte(`{"format":"solaria.sh/state/v9"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Read("dev"); err == nil {
		t.Error("Read of an unknown format succeeded")
	}
}

func TestNewUUID(t *testing.T) {
	u := newUUID()
	if len(u) != 36 || u[14] != '4' {
		t.Errorf("newUUID() = %q, want a version 4 UUID", u)
	}
	if newUUID() == u {
		t.Error("two UUIDs are equal")
	}
}
