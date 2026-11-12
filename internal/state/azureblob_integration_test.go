package state

import (
	"errors"
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/lease"
)

// TestAzureBlobIntegration runs the backend against a real storage account:
// lock, write, read, a second run refused, force-unlock, then cleanup. It
// writes under a fresh prefix and deletes its blob afterwards. It only runs
// with SOLARIA_IT_AZURE_BLOB=1, and needs SOLARIA_IT_AZURE_ACCOUNT and
// SOLARIA_IT_AZURE_CONTAINER (an existing container) and credentials the
// Azure credential chain finds, such as an az login, with read and write
// access to blobs in that container.
func TestAzureBlobIntegration(t *testing.T) {
	if os.Getenv("SOLARIA_IT_AZURE_BLOB") != "1" {
		t.Skip("set SOLARIA_IT_AZURE_BLOB=1, SOLARIA_IT_AZURE_ACCOUNT and SOLARIA_IT_AZURE_CONTAINER to run against a real storage account")
	}
	cfg := AzureBlobConfig{
		Account:   os.Getenv("SOLARIA_IT_AZURE_ACCOUNT"),
		Container: os.Getenv("SOLARIA_IT_AZURE_CONTAINER"),
		Prefix:    "solaria-it/" + NewNonce(),
	}
	if cfg.Account == "" || cfg.Container == "" {
		t.Fatal("SOLARIA_IT_AZURE_BLOB needs SOLARIA_IT_AZURE_ACCOUNT and SOLARIA_IT_AZURE_CONTAINER")
	}
	mine, err := NewAzureBlob(cfg)
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := NewAzureBlob(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := mine.ctx()
		defer cancel()
		if lc, err := lease.NewBlobClient(mine.blob("it"), nil); err == nil {
			_, _ = lc.BreakLease(ctx, &lease.BlobBreakOptions{BreakPeriod: to.Ptr(int32(0))})
		}
		if _, err := mine.container.NewBlobClient(mine.name("it")).Delete(ctx, nil); err != nil {
			t.Logf("delete %s: %v", mine.where("it"), err)
		}
	})

	nonce, err := mine.Lock("it", testLock)
	if err != nil {
		t.Fatal(err)
	}
	if err := mine.Write(New("it")); err != nil {
		t.Fatal(err)
	}
	got, err := mine.Read("it")
	if err != nil || got == nil || got.Serial != 1 {
		t.Fatalf("Read = %+v, %v; want serial 1", got, err)
	}
	if envs, err := mine.Environments(); err != nil || len(envs) != 1 || envs[0] != "it" {
		t.Errorf("Environments = %v, %v; want [it]", envs, err)
	}

	_, err = theirs.Lock("it", testLock)
	var locked *LockedError
	if !errors.As(err, &locked) || locked.Holder.Nonce != nonce {
		t.Fatalf("second Lock: err = %v, want a LockedError naming %s", err, nonce)
	}
	if err := theirs.ForceUnlock("it", nonce); err != nil {
		t.Fatal(err)
	}
	if err := mine.Write(got); err == nil {
		t.Error("Write after force-unlock succeeded")
	}
	_ = mine.Unlock("it", nonce) // stops its renewals; the lease is already broken
	again, err := theirs.Lock("it", testLock)
	if err != nil {
		t.Fatalf("Lock after force-unlock: %v", err)
	}
	if err := theirs.Unlock("it", again); err != nil {
		t.Fatal(err)
	}
}
