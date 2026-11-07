// Package state records what Solaria deployed where (ADR 0009): one state
// per environment, holding each agent's digests, host binding and history.
// State never holds secret values, only reference names.
package state

import (
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/solaria-sh/solaria/internal/resolve"
)

// Format names the shape of a state file.
const Format = "solaria.sh/state/v1"

// State is one environment's state.
type State struct {
	Format string `json:"format"`
	// Lineage is fixed when the state is created. A write with another
	// lineage is refused: it belongs to a different state.
	Lineage string `json:"lineage"`
	// Serial increases with every write. A write based on an older serial
	// is refused, so a stale copy can't overwrite newer state.
	Serial      int    `json:"serial"`
	Environment string `json:"environment"`
	// Host is the environment's own host object, for providers whose host
	// keeps one that all agents share (ADR 0012).
	Host *EnvironmentEntry `json:"host,omitempty"`
	// Agents by logical ID, such as Agent/reviewer.
	Agents map[string]*Entry `json:"agents"`
}

// EnvironmentEntry is an environment's own host object, such as a Managed
// Agents environment.
type EnvironmentEntry struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	ProviderVersion string `json:"providerVersion"`
	Placement       string `json:"placement"`
	// Digest identifies the applied config, as the provider computes it.
	Digest string `json:"digest"`
	// Config is the applied config in the provider's own terms, for
	// showing what a plan changes. It never holds secret values.
	Config     map[string]any `json:"config,omitempty"`
	Binding    Binding        `json:"binding"`
	RemoteHash string         `json:"remoteHash,omitempty"`
	Source     Source         `json:"source"`
	AppliedAt  time.Time      `json:"appliedAt"`
	AppliedBy  string         `json:"appliedBy,omitempty"`
}

// New returns an empty state for an environment, with a fresh lineage.
func New(environment string) *State {
	return &State{Format: Format, Lineage: newUUID(), Environment: environment, Agents: map[string]*Entry{}}
}

// IDs returns the logical IDs in the state, sorted.
func (s *State) IDs() []string {
	ids := make([]string, 0, len(s.Agents))
	for id := range s.Agents {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// Entry is one deployed agent.
type Entry struct {
	ID              string `json:"id"`
	Provider        string `json:"provider"`
	ProviderVersion string `json:"providerVersion"`
	Placement       string `json:"placement"`
	SpecDigest      string `json:"specDigest"`
	ResolvedDigest  string `json:"resolvedDigest"`
	// Binding is what the host calls the deployed agent.
	Binding Binding `json:"binding"`
	// RemoteHash is a hash of the host object as of the last apply. A
	// refresh that finds another hash reports drift.
	RemoteHash string `json:"remoteHash,omitempty"`
	// Definition is what was applied, for showing what a plan changes.
	Definition resolve.Definition `json:"definition"`
	Source     Source             `json:"source"`
	AppliedAt  time.Time          `json:"appliedAt"`
	AppliedBy  string             `json:"appliedBy,omitempty"`
	// History holds earlier bindings, oldest first.
	History []Revision `json:"history,omitempty"`
}

// Binding identifies the host object.
type Binding struct {
	HostID  string            `json:"hostId"`
	Version string            `json:"version,omitempty"`
	Outputs map[string]string `json:"outputs,omitempty"`
}

// Source is the commit an apply was made from.
type Source struct {
	Commit string `json:"commit,omitempty"`
	// Dirty means the worktree had uncommitted changes, so Commit alone
	// doesn't reproduce what was applied.
	Dirty bool `json:"dirty,omitempty"`
}

// Revision is an earlier binding of an entry.
type Revision struct {
	SpecDigest     string    `json:"specDigest"`
	ResolvedDigest string    `json:"resolvedDigest"`
	Binding        Binding   `json:"binding"`
	Source         Source    `json:"source"`
	AppliedAt      time.Time `json:"appliedAt"`
}

// Revision returns the entry's current binding as a history item.
func (e *Entry) Revision() Revision {
	return Revision{
		SpecDigest:     e.SpecDigest,
		ResolvedDigest: e.ResolvedDigest,
		Binding:        e.Binding,
		Source:         e.Source,
		AppliedAt:      e.AppliedAt,
	}
}

// ErrStale means a write was based on an outdated or foreign state.
var ErrStale = errors.New("state changed since it was read")

// LockedError means another process holds an environment's lock.
type LockedError struct {
	Environment string
	Holder      LockInfo
}

func (e *LockedError) Error() string {
	h := e.Holder
	return fmt.Sprintf("environment %q is locked by %s (%s, since %s, lock %s); if no one is using it, run: solaria force-unlock -e %s %s",
		e.Environment, h.Who, h.Operation, h.Created.Format(time.RFC3339), h.Nonce, e.Environment, h.Nonce)
}

// LockInfo describes who holds a lock.
type LockInfo struct {
	Nonce     string    `json:"nonce"`
	Operation string    `json:"operation"`
	Who       string    `json:"who"`
	Created   time.Time `json:"created"`
}

// Backend stores states. The local backend is the only one in v0; remote
// backends with real locking come later.
type Backend interface {
	// Lock takes the environment's lock and returns its nonce.
	Lock(env string, info LockInfo) (nonce string, err error)
	// Unlock releases a lock this process holds.
	Unlock(env, nonce string) error
	// ForceUnlock releases a lock whatever holds it, if the nonce matches.
	ForceUnlock(env, nonce string) error
	// Read returns the environment's state, or nil if it has none.
	Read(env string) (*State, error)
	// Write stores s, which must have been read (or created) from the
	// current state: same lineage and serial. It increments s.Serial.
	Write(s *State) error
	// Environments lists the environments that have state, sorted.
	Environments() ([]string, error)
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// NewNonce returns a random lock nonce.
func NewNonce() string { return newUUID() }
