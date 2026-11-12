package state

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
)

// fakeBlobs is the part of the Azure Blob REST API the backend uses: block
// blob upload and download, properties, metadata, leases and listing, in
// one container named "state".
type fakeBlobs struct {
	mu     sync.Mutex
	blobs  map[string]*fakeBlob
	renews int
	tokens []string
}

type fakeBlob struct {
	data     []byte
	metadata map[string]string
	etag     int
	leaseID  string
	expires  time.Time
	broken   bool
}

func (b *fakeBlob) leased() bool {
	return b.leaseID != "" && !b.broken && time.Now().Before(b.expires)
}

func newFakeBlobs(t *testing.T) (*fakeBlobs, *httptest.Server) {
	f := &fakeBlobs{blobs: map[string]*fakeBlob{}}
	srv := httptest.NewTLSServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

// backend returns an AzureBlob on the fake, as another process would have.
func (f *fakeBlobs) backend(t *testing.T, srv *httptest.Server, prefix string) *AzureBlob {
	t.Helper()
	opts := &container.ClientOptions{ClientOptions: azcore.ClientOptions{
		Transport: srv.Client(),
		Retry:     policy.RetryOptions{MaxRetries: -1},
	}}
	b, err := newAzureBlob(srv.URL+"/state", prefix, fakeCredential{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// expire ends a blob's lease as if its holder had stopped renewing it.
func (f *fakeBlobs) expire(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blobs[name].expires = time.Now().Add(-time.Second)
}

type fakeCredential struct{}

func (fakeCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "fake-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func fail(w http.ResponseWriter, status int, code string) {
	w.Header().Set("x-ms-error-code", code)
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?><Error><Code>%s</Code><Message>%s</Message></Error>`, code, code)
}

func (f *fakeBlobs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, r.Header.Get("Authorization"))
	w.Header().Set("x-ms-version", r.Header.Get("x-ms-version"))
	cont, name, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if cont != "state" {
		fail(w, http.StatusNotFound, "ContainerNotFound")
		return
	}
	q := r.URL.Query()
	if name == "" {
		if r.Method == http.MethodGet && q.Get("comp") == "list" {
			f.list(w, q.Get("prefix"))
			return
		}
		fail(w, http.StatusBadRequest, "UnsupportedHttpVerb")
		return
	}
	b := f.blobs[name]
	switch {
	case r.Method == http.MethodPut && q.Get("comp") == "lease":
		f.lease(w, r, b)
	case b == nil && r.Method != http.MethodPut:
		fail(w, http.StatusNotFound, "BlobNotFound")
	case r.Method == http.MethodPut && q.Get("comp") == "metadata":
		if !checkLease(w, r, b) {
			return
		}
		b.metadata = metadataOf(r)
		w.Header().Set("ETag", strconv.Quote(strconv.Itoa(b.etag)))
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodPut:
		if b != nil && r.Header.Get("If-None-Match") == "*" {
			fail(w, http.StatusConflict, "BlobAlreadyExists")
			return
		}
		if b == nil {
			b = &fakeBlob{}
		}
		if !checkLease(w, r, b) {
			return
		}
		data, _ := io.ReadAll(r.Body)
		b.data, b.metadata = data, metadataOf(r)
		b.etag++
		f.blobs[name] = b
		w.Header().Set("ETag", strconv.Quote(strconv.Itoa(b.etag)))
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		h := w.Header()
		h.Set("ETag", strconv.Quote(strconv.Itoa(b.etag)))
		h.Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		h.Set("x-ms-blob-type", "BlockBlob")
		h.Set("Content-Length", strconv.Itoa(len(b.data)))
		for k, v := range b.metadata {
			h.Set("x-ms-meta-"+k, v)
		}
		if b.leased() {
			h.Set("x-ms-lease-state", "leased")
			h.Set("x-ms-lease-status", "locked")
		} else {
			h.Set("x-ms-lease-state", "available")
			h.Set("x-ms-lease-status", "unlocked")
		}
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(b.data)
		}
	default:
		fail(w, http.StatusBadRequest, "UnsupportedHttpVerb")
	}
}

// checkLease applies a write's lease condition, as the service does.
func checkLease(w http.ResponseWriter, r *http.Request, b *fakeBlob) bool {
	id := r.Header.Get("x-ms-lease-id")
	switch {
	case b.leased() && id == "":
		fail(w, http.StatusPreconditionFailed, "LeaseIdMissing")
	case b.leased() && id != b.leaseID:
		fail(w, http.StatusPreconditionFailed, "LeaseIdMismatchWithBlobOperation")
	case !b.leased() && id != "":
		fail(w, http.StatusPreconditionFailed, "LeaseNotPresentWithBlobOperation")
	default:
		return true
	}
	return false
}

func (f *fakeBlobs) lease(w http.ResponseWriter, r *http.Request, b *fakeBlob) {
	if b == nil {
		fail(w, http.StatusNotFound, "BlobNotFound")
		return
	}
	id := r.Header.Get("x-ms-lease-id")
	switch r.Header.Get("x-ms-lease-action") {
	case "acquire":
		if b.leased() {
			fail(w, http.StatusConflict, "LeaseAlreadyPresent")
			return
		}
		secs, _ := strconv.Atoi(r.Header.Get("x-ms-lease-duration"))
		b.leaseID, b.broken, b.expires = r.Header.Get("x-ms-proposed-lease-id"), false, time.Now().Add(time.Duration(secs)*time.Second)
		w.Header().Set("x-ms-lease-id", b.leaseID)
		w.WriteHeader(http.StatusCreated)
	case "renew":
		if id != b.leaseID || b.broken {
			fail(w, http.StatusConflict, "LeaseIdMismatchWithLeaseOperation")
			return
		}
		f.renews++
		b.expires = time.Now().Add(time.Minute)
		w.Header().Set("x-ms-lease-id", id)
		w.WriteHeader(http.StatusOK)
	case "release":
		if id != b.leaseID {
			fail(w, http.StatusConflict, "LeaseIdMismatchWithLeaseOperation")
			return
		}
		b.leaseID = ""
		w.WriteHeader(http.StatusOK)
	case "break":
		if !b.leased() {
			fail(w, http.StatusConflict, "LeaseNotPresentWithLeaseOperation")
			return
		}
		b.broken = true
		w.Header().Set("x-ms-lease-time", "0")
		w.WriteHeader(http.StatusAccepted)
	default:
		fail(w, http.StatusBadRequest, "InvalidHeaderValue")
	}
}

func metadataOf(r *http.Request) map[string]string {
	m := map[string]string{}
	for k, v := range r.Header {
		if name, ok := strings.CutPrefix(strings.ToLower(k), "x-ms-meta-"); ok {
			m[name] = v[0]
		}
	}
	return m
}

func (f *fakeBlobs) list(w http.ResponseWriter, prefix string) {
	type props struct {
		ContentLength int `xml:"Content-Length"`
	}
	type item struct {
		Name       string
		Properties props
	}
	var items []item
	for name, b := range f.blobs {
		if strings.HasPrefix(name, prefix) {
			items = append(items, item{Name: name, Properties: props{ContentLength: len(b.data)}})
		}
	}
	out, _ := xml.Marshal(struct {
		XMLName xml.Name `xml:"EnumerationResults"`
		Prefix  string
		Blobs   []item `xml:"Blobs>Blob"`
	}{Prefix: prefix, Blobs: items})
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write(out)
}

var testLock = LockInfo{Operation: "apply", Who: "ci", Created: time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)}

func TestAzureBlobWriteAndRead(t *testing.T) {
	f, srv := newFakeBlobs(t)
	b := f.backend(t, srv, "teams/platform/")
	got, err := b.Read("dev")
	if err != nil || got != nil {
		t.Fatalf("Read of a new environment = (%v, %v), want (nil, nil)", got, err)
	}
	nonce, err := b.Lock("dev", testLock)
	if err != nil {
		t.Fatal(err)
	}
	// Locking created an empty blob, which is still no state.
	if got, err := b.Read("dev"); err != nil || got != nil {
		t.Fatalf("Read after Lock = (%v, %v), want (nil, nil)", got, err)
	}
	if envs, err := b.Environments(); err != nil || len(envs) != 0 {
		t.Errorf("Environments after Lock = %v, %v; want none", envs, err)
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
	if err := b.Unlock("dev", nonce); err != nil {
		t.Fatal(err)
	}

	f.mu.Lock()
	blob := f.blobs["teams/platform/dev.json"]
	f.mu.Unlock()
	if blob == nil || !strings.Contains(string(blob.data), `"format": "solaria.sh/state/v1"`) {
		t.Fatalf("no state JSON at teams/platform/dev.json: %v", blob)
	}
	if blob.leased() || len(blob.metadata) != 0 {
		t.Errorf("after Unlock the blob is leased (%v) or keeps lock metadata %v", blob.leased(), blob.metadata)
	}
	f.blobs["teams/platform/nested/x.json"] = &fakeBlob{data: []byte("{}")}
	f.blobs["teams/other.json"] = &fakeBlob{data: []byte("{}")}
	envs, err := b.Environments()
	if err != nil || len(envs) != 1 || envs[0] != "dev" {
		t.Errorf("Environments = %v, %v; want [dev]", envs, err)
	}
	for _, tok := range f.tokens {
		if tok != "Bearer fake-token" {
			t.Fatalf("a request carried Authorization %q, want the credential's bearer token", tok)
		}
	}
}

func TestAzureBlobWriteNeedsTheLock(t *testing.T) {
	f, srv := newFakeBlobs(t)
	b := f.backend(t, srv, "")
	err := b.Write(New("dev"))
	if err == nil || !strings.Contains(err.Error(), "doesn't hold its lock") {
		t.Errorf("Write without Lock: err = %v", err)
	}
}

func TestAzureBlobRefusesStaleWrites(t *testing.T) {
	f, srv := newFakeBlobs(t)
	b := f.backend(t, srv, "")
	if _, err := b.Lock("dev", testLock); err != nil {
		t.Fatal(err)
	}
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
	other := New("dev")
	other.Serial = 2
	if err := b.Write(other); !errors.Is(err, ErrStale) || !strings.Contains(err.Error(), "lineage") {
		t.Errorf("write with another lineage: err = %v, want ErrStale", err)
	}
	f.mu.Lock()
	f.blobs["dev.json"].data = nil
	f.mu.Unlock()
	if err := b.Write(first); !errors.Is(err, ErrStale) || !strings.Contains(err.Error(), "removed") {
		t.Errorf("write after the state was emptied: err = %v, want ErrStale", err)
	}
}

func TestAzureBlobLock(t *testing.T) {
	f, srv := newFakeBlobs(t)
	mine, theirs := f.backend(t, srv, ""), f.backend(t, srv, "")
	nonce, err := mine.Lock("dev", testLock)
	if err != nil {
		t.Fatal(err)
	}
	if err := mine.Write(New("dev")); err != nil {
		t.Fatal(err)
	}

	_, err = theirs.Lock("dev", LockInfo{Operation: "destroy"})
	var locked *LockedError
	if !errors.As(err, &locked) || locked.Holder.Nonce != nonce || locked.Holder.Operation != "apply" || locked.Holder.Who != "ci" {
		t.Fatalf("second Lock: err = %v, want a LockedError naming the holder", err)
	}
	if err := theirs.Unlock("dev", nonce); err == nil {
		t.Error("Unlock of a lock another process holds succeeded")
	}
	if err := theirs.ForceUnlock("dev", "wrong"); err == nil || !strings.Contains(err.Error(), "not wrong") {
		t.Errorf("ForceUnlock with the wrong nonce: err = %v", err)
	}
	if err := theirs.ForceUnlock("dev", nonce); err != nil {
		t.Fatal(err)
	}
	if err := theirs.ForceUnlock("dev", nonce); err == nil || !strings.Contains(err.Error(), "not locked") {
		t.Errorf("ForceUnlock of an unlocked environment: err = %v", err)
	}
	// The broken lease no longer lets the first process write.
	st, _ := mine.Read("dev")
	if err := mine.Write(st); err == nil || !strings.Contains(err.Error(), "LeaseNotPresentWithBlobOperation") {
		t.Errorf("Write after force-unlock: err = %v", err)
	}

	again, err := theirs.Lock("dev", testLock)
	if err != nil {
		t.Fatalf("Lock after force-unlock: %v", err)
	}
	if err := theirs.Unlock("dev", again); err != nil {
		t.Fatal(err)
	}
}

func TestAzureBlobStoppedRunsLockExpires(t *testing.T) {
	f, srv := newFakeBlobs(t)
	stopped, next := f.backend(t, srv, ""), f.backend(t, srv, "")
	if _, err := stopped.Lock("dev", testLock); err != nil {
		t.Fatal(err)
	}
	f.expire("dev.json")
	nonce, err := next.Lock("dev", testLock)
	if err != nil {
		t.Fatalf("Lock after the holder's lease expired: %v", err)
	}
	if err := stopped.Write(New("dev")); err == nil {
		t.Error("the stopped run wrote under another run's lease")
	}
	if err := next.Unlock("dev", nonce); err != nil {
		t.Fatal(err)
	}
}

func TestAzureBlobRenewsTheLease(t *testing.T) {
	f, srv := newFakeBlobs(t)
	b := f.backend(t, srv, "")
	b.renewEvery = 5 * time.Millisecond
	nonce, err := b.Lock("dev", testLock)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		n := f.renews
		f.mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the lease was renewed %d times in 5s", n)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := b.Unlock("dev", nonce); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	n := f.renews
	f.mu.Unlock()
	time.Sleep(30 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.renews != n {
		t.Errorf("renewals continued after Unlock: %d, then %d", n, f.renews)
	}
}

func TestAzureBlobForeignLease(t *testing.T) {
	f, srv := newFakeBlobs(t)
	b := f.backend(t, srv, "")
	f.blobs["dev.json"] = &fakeBlob{leaseID: "someone", expires: time.Now().Add(time.Hour)}
	_, err := b.Lock("dev", testLock)
	if err == nil || !strings.Contains(err.Error(), "a lease Solaria didn't take") {
		t.Errorf("Lock of a blob leased by something else: err = %v", err)
	}
}

func TestAzureBlobErrors(t *testing.T) {
	f, srv := newFakeBlobs(t)
	b := f.backend(t, srv, "")
	f.blobs["dev.json"] = &fakeBlob{data: []byte(`{"format":"solaria.sh/state/v9"}`)}
	if _, err := b.Read("dev"); err == nil || !strings.Contains(err.Error(), "format") {
		t.Errorf("Read of an unknown format: err = %v", err)
	}

	missing, err := newAzureBlob(srv.URL+"/nowhere", "", fakeCredential{}, &container.ClientOptions{ClientOptions: azcore.ClientOptions{Transport: srv.Client(), Retry: policy.RetryOptions{MaxRetries: -1}}})
	if err != nil {
		t.Fatal(err)
	}
	want := "container " + srv.URL + "/nowhere doesn't exist"
	if _, err := missing.Read("dev"); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Read from a missing container: err = %v, want %q", err, want)
	}
	if _, err := missing.Lock("dev", testLock); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Lock in a missing container: err = %v, want %q", err, want)
	}
}
