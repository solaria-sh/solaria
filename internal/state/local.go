package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Local stores state files in a directory, usually .solaria/state.
type Local struct {
	Dir string
}

var _ Backend = (*Local)(nil)

func (l *Local) statePath(env string) string { return filepath.Join(l.Dir, env+".json") }
func (l *Local) lockPath(env string) string  { return filepath.Join(l.Dir, env+".lock") }

func (l *Local) ensureDir() error {
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return err
	}
	// Keep state out of git without asking users to configure anything.
	// The parent is .solaria when the default directory is used.
	if filepath.Base(l.Dir) == "state" {
		ignore := filepath.Join(filepath.Dir(l.Dir), ".gitignore")
		if _, err := os.Stat(ignore); errors.Is(err, fs.ErrNotExist) {
			_ = os.WriteFile(ignore, []byte("# Created by solaria: local state stays out of git.\n*\n"), 0o644)
		}
	}
	return nil
}

// Lock creates the lock file exclusively, so only one process can hold it.
func (l *Local) Lock(env string, info LockInfo) (string, error) {
	if err := l.ensureDir(); err != nil {
		return "", fmt.Errorf("create state directory: %w", err)
	}
	info.Nonce = NewNonce()
	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(l.lockPath(env), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		holder, rerr := l.readLock(env)
		if rerr != nil {
			return "", fmt.Errorf("target %q is locked, and its lock file can't be read: %w", env, rerr)
		}
		return "", &LockedError{Target: env, Holder: holder}
	}
	if err != nil {
		return "", fmt.Errorf("lock target %q: %w", env, err)
	}
	_, werr := f.Write(append(b, '\n'))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(l.lockPath(env))
		return "", fmt.Errorf("lock target %q: %w", env, werr)
	}
	return info.Nonce, nil
}

func (l *Local) readLock(env string) (LockInfo, error) {
	var info LockInfo
	b, err := os.ReadFile(l.lockPath(env))
	if err != nil {
		return info, err
	}
	err = json.Unmarshal(b, &info)
	return info, err
}

// Unlock removes the lock if it still holds nonce.
func (l *Local) Unlock(env, nonce string) error {
	return l.unlock(env, nonce)
}

// ForceUnlock removes the lock if it holds nonce, whoever took it.
func (l *Local) ForceUnlock(env, nonce string) error {
	return l.unlock(env, nonce)
}

func (l *Local) unlock(env, nonce string) error {
	info, err := l.readLock(env)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("target %q is not locked", env)
	}
	if err != nil {
		return fmt.Errorf("read lock of target %q: %w", env, err)
	}
	if info.Nonce != nonce {
		return fmt.Errorf("target %q is locked with %s, not %s", env, info.Nonce, nonce)
	}
	return os.Remove(l.lockPath(env))
}

// Read loads the state, or returns nil if the target has none.
func (l *Local) Read(env string) (*State, error) {
	b, err := os.ReadFile(l.statePath(env))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	return decode(b, l.statePath(env))
}

// Write checks lineage and serial against the file, then replaces it
// atomically.
func (l *Local) Write(s *State) error {
	current, err := l.Read(s.Target)
	if err != nil {
		return err
	}
	if err := checkWrite(current, s, "on disk"); err != nil {
		return err
	}
	if err := l.ensureDir(); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	next := *s
	next.Serial++
	b, err := json.MarshalIndent(&next, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(l.Dir, "."+s.Target+".*.tmp")
	if err != nil {
		return fmt.Errorf("write state: %w", err)
	}
	_, werr := tmp.Write(append(b, '\n'))
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), l.statePath(s.Target))
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("write state: %w", werr)
	}
	s.Serial = next.Serial
	return nil
}

// Targets lists targets with a state file.
func (l *Local) Targets() ([]string, error) {
	entries, err := os.ReadDir(l.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var envs []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".json"); ok && !e.IsDir() && !strings.HasPrefix(name, ".") {
			envs = append(envs, name)
		}
	}
	slices.Sort(envs)
	return envs, nil
}
