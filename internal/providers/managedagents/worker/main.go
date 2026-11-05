// Command solaria-worker serves a Managed Agents self-hosted environment:
// it polls the environment's work queue and runs the agents' tools in this
// container, with the SDK's EnvironmentWorker. The managed-agents provider
// builds it into the worker image and runs one container per environment.
//
// It reads ANTHROPIC_ENVIRONMENT_ID and ANTHROPIC_ENVIRONMENT_KEY. The key
// only reaches this environment's queue; the container never holds an API
// key, since agent tool calls run here.
//
// The image builds it as module solaria-worker, from go.mod.txt and
// go.sum.txt. After changing the SDK version in Solaria's go.mod, regenerate
// them in an empty folder holding a copy of this file:
//
//	go mod init solaria-worker
//	go get github.com/anthropics/anthropic-sdk-go/v2@<version>
//	go mod tidy
//
// and copy go.mod and go.sum back here as go.mod.txt and go.sum.txt.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/lib/environments"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "solaria-worker:", err)
		os.Exit(1)
	}
}

func run() error {
	id, key := os.Getenv("ANTHROPIC_ENVIRONMENT_ID"), os.Getenv("ANTHROPIC_ENVIRONMENT_KEY")
	if id == "" || key == "" {
		return errors.New("ANTHROPIC_ENVIRONMENT_ID and ANTHROPIC_ENVIRONMENT_KEY must be set")
	}
	// Cancelling, rather than killing, lets the worker finish the work item
	// it holds and upload changed memory files.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := anthropic.NewClient(option.WithAuthToken(key))
	worker := environments.NewEnvironmentWorker(client, environments.EnvironmentWorkerOptions{
		EnvironmentID:  id,
		EnvironmentKey: key,
		Workdir:        "/workspace",
	})
	if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
