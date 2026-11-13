package state

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/lease"
)

// lockMetadata is the blob metadata key that holds the lock's LockInfo,
// base64-encoded JSON, while a lease is held.
const lockMetadata = "solarialock"

// AzureBlobConfig names where an Azure Blob backend keeps states.
type AzureBlobConfig struct {
	// Account is the storage account's name.
	Account string
	// Container holds the state blobs. Solaria doesn't create it.
	Container string
	// Prefix is a folder in the container, or empty.
	Prefix string
}

// AzureBlob stores each target's state as a blob in an Azure Storage
// container, <prefix>/<target>.json, holding the same JSON as a local
// state file (ADR 0009). The lock is a lease on that blob, renewed until
// Unlock, so a stopped run's lock expires on its own within a lease
// period. Credentials come from the Azure credential chain.
type AzureBlob struct {
	container *container.Client
	prefix    string
	// url is the container's URL, for messages.
	url string

	// leaseSeconds is how long a lease lasts without renewal (15 to 60),
	// and renewEvery how often Lock's goroutine renews it.
	leaseSeconds int32
	renewEvery   time.Duration
	// timeout bounds each call to Azure.
	timeout time.Duration

	mu   sync.Mutex
	held map[string]*heldLease
}

// heldLease is a lease this process holds on a target's blob.
type heldLease struct {
	nonce    string
	lease    *lease.BlobClient
	metadata map[string]*string
	stop     chan struct{}
	done     chan struct{}
	// renewErr is the last renewal's error, nil once one succeeds again.
	renewErr error
}

var _ Backend = (*AzureBlob)(nil)

// NewAzureBlob returns a backend for cfg that authenticates with the Azure
// credential chain: environment variables, workload identity, managed
// identity, then the Azure CLI and Azure Developer CLI logins. Nothing is
// contacted until the first call.
func NewAzureBlob(cfg AzureBlobConfig) (*AzureBlob, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure credentials for state in storage account %s: %w", cfg.Account, err)
	}
	url := fmt.Sprintf("https://%s.blob.core.windows.net/%s", cfg.Account, cfg.Container)
	return newAzureBlob(url, cfg.Prefix, cred, nil)
}

func newAzureBlob(containerURL, prefix string, cred azcore.TokenCredential, opts *container.ClientOptions) (*AzureBlob, error) {
	c, err := container.NewClient(containerURL, cred, opts)
	if err != nil {
		return nil, fmt.Errorf("state container %s: %w", containerURL, err)
	}
	return &AzureBlob{
		container:    c,
		prefix:       strings.Trim(prefix, "/"),
		url:          containerURL,
		leaseSeconds: 60,
		renewEvery:   20 * time.Second,
		timeout:      2 * time.Minute,
		held:         map[string]*heldLease{},
	}, nil
}

func (b *AzureBlob) name(env string) string { return path.Join(b.prefix, env+".json") }

func (b *AzureBlob) where(env string) string { return b.url + "/" + b.name(env) }

func (b *AzureBlob) blob(env string) *blockblob.Client {
	return b.container.NewBlockBlobClient(b.name(env))
}

func (b *AzureBlob) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), b.timeout)
}

// Lock leases the target's blob, creating an empty one first if the
// target has no state yet: a lease needs a blob, and Read treats an
// empty one as no state. The lease ID is the nonce.
func (b *AzureBlob) Lock(env string, info LockInfo) (string, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	bb := b.blob(env)
	_, err := bb.Upload(ctx, nopCloser(nil), &blockblob.UploadOptions{
		AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{IfNoneMatch: to.Ptr(azcore.ETagAny)}},
	})
	if err != nil && !bloberror.HasCode(err, bloberror.BlobAlreadyExists, bloberror.ConditionNotMet, bloberror.LeaseIDMissing) {
		return "", b.fail(err, "lock target %q", env)
	}

	info.Nonce = NewNonce()
	lc, err := lease.NewBlobClient(bb, &lease.BlobClientOptions{LeaseID: to.Ptr(info.Nonce)})
	if err != nil {
		return "", fmt.Errorf("lock target %q: %w", env, err)
	}
	if _, err := lc.AcquireLease(ctx, b.leaseSeconds, nil); err != nil {
		if bloberror.HasCode(err, bloberror.LeaseAlreadyPresent) {
			return "", b.locked(ctx, env)
		}
		return "", b.fail(err, "lock target %q", env)
	}
	raw, err := json.Marshal(info)
	if err != nil {
		return "", err
	}
	metadata := map[string]*string{lockMetadata: to.Ptr(base64.StdEncoding.EncodeToString(raw))}
	if _, err := bb.SetMetadata(ctx, metadata, &blob.SetMetadataOptions{AccessConditions: leased(info.Nonce)}); err != nil {
		_, _ = lc.ReleaseLease(ctx, nil)
		return "", b.fail(err, "lock target %q", env)
	}

	h := &heldLease{nonce: info.Nonce, lease: lc, metadata: metadata, stop: make(chan struct{}), done: make(chan struct{})}
	b.mu.Lock()
	b.held[env] = h
	b.mu.Unlock()
	go b.renew(h)
	return info.Nonce, nil
}

// renew keeps h's lease until Unlock stops it.
func (b *AzureBlob) renew(h *heldLease) {
	defer close(h.done)
	t := time.NewTicker(b.renewEvery)
	defer t.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-t.C:
			ctx, cancel := b.ctx()
			_, err := h.lease.RenewLease(ctx, nil)
			cancel()
			b.mu.Lock()
			h.renewErr = err
			b.mu.Unlock()
		}
	}
}

// locked describes who holds env's lease.
func (b *AzureBlob) locked(ctx context.Context, env string) error {
	info, ok, err := b.lockInfo(ctx, env)
	if err != nil {
		return fmt.Errorf("target %q is locked, and its lock information can't be read: %w", env, err)
	}
	if !ok {
		return fmt.Errorf("target %q is locked by a lease Solaria didn't take on %s; if no one is using it, break the blob's lease in Azure", env, b.where(env))
	}
	return &LockedError{Target: env, Holder: info}
}

// lockInfo reads the lock information of env's blob, and whether its
// lease is held.
func (b *AzureBlob) lockInfo(ctx context.Context, env string) (info LockInfo, leasedNow bool, err error) {
	props, err := b.blob(env).GetProperties(ctx, nil)
	if bloberror.HasCode(err, bloberror.BlobNotFound) {
		return info, false, nil
	}
	if err != nil {
		return info, false, b.fail(err, "read lock of target %q", env)
	}
	if props.LeaseState == nil || *props.LeaseState != lease.StateTypeLeased {
		return info, false, nil
	}
	for k, v := range props.Metadata {
		if strings.EqualFold(k, lockMetadata) && v != nil {
			raw, err := base64.StdEncoding.DecodeString(*v)
			if err == nil {
				err = json.Unmarshal(raw, &info)
			}
			if err != nil {
				return info, true, fmt.Errorf("lock information on %s: %w", b.where(env), err)
			}
			return info, true, nil
		}
	}
	return info, false, nil
}

// Unlock releases the lease this process took with nonce.
func (b *AzureBlob) Unlock(env, nonce string) error {
	b.mu.Lock()
	h := b.held[env]
	if h == nil || h.nonce != nonce {
		b.mu.Unlock()
		return fmt.Errorf("target %q isn't locked by this run with %s", env, nonce)
	}
	delete(b.held, env)
	b.mu.Unlock()
	close(h.stop)
	<-h.done

	ctx, cancel := b.ctx()
	defer cancel()
	// Clear the lock information first: it describes the lease.
	_, _ = b.blob(env).SetMetadata(ctx, nil, &blob.SetMetadataOptions{AccessConditions: leased(nonce)})
	if _, err := h.lease.ReleaseLease(ctx, nil); err != nil {
		return b.fail(err, "unlock target %q", env)
	}
	return nil
}

// ForceUnlock breaks the blob's lease if the lock holds nonce, whoever
// took it.
func (b *AzureBlob) ForceUnlock(env, nonce string) error {
	ctx, cancel := b.ctx()
	defer cancel()
	props, err := b.blob(env).GetProperties(ctx, nil)
	if err != nil && !bloberror.HasCode(err, bloberror.BlobNotFound) {
		return b.fail(err, "read lock of target %q", env)
	}
	if err != nil || props.LeaseState == nil || *props.LeaseState != lease.StateTypeLeased {
		return fmt.Errorf("target %q is not locked", env)
	}
	info, ok, err := b.lockInfo(ctx, env)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("target %q is locked by a lease Solaria didn't take on %s; break it in Azure if no one is using it", env, b.where(env))
	}
	if info.Nonce != nonce {
		return fmt.Errorf("target %q is locked with %s, not %s", env, info.Nonce, nonce)
	}
	lc, err := lease.NewBlobClient(b.blob(env), nil)
	if err != nil {
		return fmt.Errorf("unlock target %q: %w", env, err)
	}
	if _, err := lc.BreakLease(ctx, &lease.BlobBreakOptions{BreakPeriod: to.Ptr(int32(0))}); err != nil {
		return b.fail(err, "unlock target %q", env)
	}
	return nil
}

// Read downloads the target's blob. A missing or empty blob is no
// state.
func (b *AzureBlob) Read(env string) (*State, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	resp, err := b.blob(env).DownloadStream(ctx, nil)
	if bloberror.HasCode(err, bloberror.BlobNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, b.fail(err, "read state of target %q", env)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read state %s: %w", b.where(env), err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return decode(raw, b.where(env))
}

// Write checks lineage and serial against the blob, then replaces it under
// the lease this process holds.
func (b *AzureBlob) Write(s *State) error {
	env := s.Target
	b.mu.Lock()
	h := b.held[env]
	var renewErr error
	if h != nil {
		renewErr = h.renewErr
	}
	b.mu.Unlock()
	if h == nil {
		return fmt.Errorf("write state of target %q: this run doesn't hold its lock", env)
	}
	current, err := b.Read(env)
	if err != nil {
		return err
	}
	if err := checkWrite(current, s, "in "+b.where(env)); err != nil {
		return err
	}

	next := *s
	next.Serial++
	raw, err := json.MarshalIndent(&next, "", "  ")
	if err != nil {
		return err
	}
	ctx, cancel := b.ctx()
	defer cancel()
	_, err = b.blob(env).Upload(ctx, nopCloser(append(raw, '\n')), &blockblob.UploadOptions{
		HTTPHeaders:      &blob.HTTPHeaders{BlobContentType: to.Ptr("application/json")},
		Metadata:         h.metadata,
		AccessConditions: leased(h.nonce),
	})
	if err != nil {
		if renewErr != nil {
			return fmt.Errorf("%w; the lock was probably lost, since renewing its lease failed: %w", b.fail(err, "write state of target %q", env), renewErr)
		}
		return b.fail(err, "write state of target %q", env)
	}
	s.Serial = next.Serial
	return nil
}

// Targets lists the non-empty state blobs directly under the prefix.
func (b *AzureBlob) Targets() ([]string, error) {
	ctx, cancel := b.ctx()
	defer cancel()
	prefix := ""
	if b.prefix != "" {
		prefix = b.prefix + "/"
	}
	var envs []string
	pager := b.container.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{Prefix: &prefix})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, b.fail(err, "list states")
		}
		for _, item := range page.Segment.BlobItems {
			if item.Name == nil || item.Properties == nil || item.Properties.ContentLength == nil || *item.Properties.ContentLength == 0 {
				continue
			}
			name, ok := strings.CutSuffix(strings.TrimPrefix(*item.Name, prefix), ".json")
			if ok && name != "" && !strings.Contains(name, "/") {
				envs = append(envs, name)
			}
		}
	}
	slices.Sort(envs)
	return envs, nil
}

// fail wraps an Azure error with what was being done, keeping only the
// status and error code of a service error: its full text repeats the
// request.
func (b *AzureBlob) fail(err error, format string, args ...any) error {
	what := fmt.Sprintf(format, args...)
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		switch {
		case re.ErrorCode == string(bloberror.ContainerNotFound):
			return fmt.Errorf("%s: container %s doesn't exist; create it, Solaria doesn't", what, b.url)
		case re.ErrorCode != "":
			return fmt.Errorf("%s: %s answered %d %s", what, b.url, re.StatusCode, re.ErrorCode)
		default:
			return fmt.Errorf("%s: %s answered %d", what, b.url, re.StatusCode)
		}
	}
	return fmt.Errorf("%s: %w", what, err)
}

func leased(id string) *blob.AccessConditions {
	return &blob.AccessConditions{LeaseAccessConditions: &blob.LeaseAccessConditions{LeaseID: to.Ptr(id)}}
}

type readSeekNopCloser struct{ *bytes.Reader }

func (readSeekNopCloser) Close() error { return nil }

func nopCloser(b []byte) io.ReadSeekCloser { return readSeekNopCloser{bytes.NewReader(b)} }
