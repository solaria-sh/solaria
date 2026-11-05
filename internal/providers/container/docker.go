// Package container holds what container-based providers share: the
// docker CLI wrapper, and the Dockerfile stages of the Solaria sandbox base
// (ADR 0007).
package container

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// Docker is the part of the docker CLI that builds and keeps images. Tests
// replace it.
type Docker interface {
	// Build builds an image, writing progress to log.
	Build(ctx context.Context, req BuildRequest, log io.Writer) error
	// Inspect returns the image a reference points to, or nil when there
	// is none.
	Inspect(ctx context.Context, ref string) (*Image, error)
	// Remove removes an image reference. A reference that doesn't exist
	// isn't an error.
	Remove(ctx context.Context, ref string) error
}

// BuildRequest is one docker build.
type BuildRequest struct {
	// Context is the build context folder.
	Context string
	// Dockerfile is the Dockerfile's path.
	Dockerfile string
	Tag        string
	Platform   string
	Labels     map[string]string
}

// Image is what Inspect reports.
type Image struct {
	ID     string
	Labels map[string]string
}

// CLI runs the docker command found on PATH. Docker-compatible CLIs with
// BuildKit (OrbStack, Colima, Podman's docker alias) work as well.
type CLI struct {
	// Command is the program to run. Empty means docker.
	Command string
}

func (c CLI) command() string {
	if c.Command == "" {
		return "docker"
	}
	return c.Command
}

// Build runs docker build.
func (c CLI) Build(ctx context.Context, req BuildRequest, log io.Writer) error {
	args := []string{"build", "--progress", "plain", "--platform", req.Platform, "--tag", req.Tag, "--file", req.Dockerfile}
	for _, k := range slices.Sorted(maps.Keys(req.Labels)) {
		args = append(args, "--label", k+"="+req.Labels[k])
	}
	args = append(args, req.Context)
	cmd := exec.CommandContext(ctx, c.command(), args...)
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build of %s: %w (the build output above says why)", req.Tag, c.explain(err))
	}
	return nil
}

// Inspect runs docker image inspect.
func (c CLI) Inspect(ctx context.Context, ref string) (*Image, error) {
	out, stderr, err := c.run(ctx, "image", "inspect", "--format", "{{json .}}", ref)
	if err != nil {
		if notFound(stderr) {
			return nil, nil
		}
		return nil, fmt.Errorf("docker image inspect %s: %w: %s", ref, c.explain(err), strings.TrimSpace(stderr))
	}
	var v struct {
		ID     string `json:"Id"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("docker image inspect %s: %w", ref, err)
	}
	return &Image{ID: v.ID, Labels: v.Config.Labels}, nil
}

// Remove runs docker image rm.
func (c CLI) Remove(ctx context.Context, ref string) error {
	_, stderr, err := c.run(ctx, "image", "rm", ref)
	if err != nil && !notFound(stderr) {
		return fmt.Errorf("docker image rm %s: %w: %s", ref, c.explain(err), strings.TrimSpace(stderr))
	}
	return nil
}

// Container is what InspectContainer reports.
type Container struct {
	ID      string
	Image   string
	Running bool
	Labels  map[string]string
}

// RunRequest is one detached docker run of a long-lived container.
type RunRequest struct {
	Name     string
	Image    string
	Platform string
	// Memory is docker's --memory, such as 8g.
	Memory string
	// Env sets variables in the container.
	Env map[string]string
	// Secrets also set variables, but their values reach docker through
	// its own environment, never its arguments, so they don't show in
	// process lists.
	Secrets map[string]string
	Labels  map[string]string
}

// RunDetached starts a container that restarts unless stopped, and
// returns its ID.
func (c CLI) RunDetached(ctx context.Context, req RunRequest) (string, error) {
	args := []string{"run", "--detach", "--restart", "unless-stopped", "--name", req.Name, "--platform", req.Platform}
	if req.Memory != "" {
		args = append(args, "--memory", req.Memory)
	}
	for _, k := range slices.Sorted(maps.Keys(req.Env)) {
		args = append(args, "--env", k+"="+req.Env[k])
	}
	env := os.Environ()
	for _, k := range slices.Sorted(maps.Keys(req.Secrets)) {
		args = append(args, "--env", k)
		env = append(env, k+"="+req.Secrets[k])
	}
	for _, k := range slices.Sorted(maps.Keys(req.Labels)) {
		args = append(args, "--label", k+"="+req.Labels[k])
	}
	args = append(args, req.Image)
	var errb bytes.Buffer
	cmd := exec.CommandContext(ctx, c.command(), args...)
	cmd.Env = env
	cmd.Stderr = &errb
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker run %s: %w: %s", req.Name, c.explain(err), strings.TrimSpace(errb.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// InspectContainer returns a container by name, or nil when there is none.
func (c CLI) InspectContainer(ctx context.Context, name string) (*Container, error) {
	out, stderr, err := c.run(ctx, "container", "inspect", "--format", "{{json .}}", name)
	if err != nil {
		if notFound(stderr) {
			return nil, nil
		}
		return nil, fmt.Errorf("docker container inspect %s: %w: %s", name, c.explain(err), strings.TrimSpace(stderr))
	}
	var v struct {
		ID    string `json:"Id"`
		Image string `json:"Image"`
		State struct {
			Running bool `json:"Running"`
		} `json:"State"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("docker container inspect %s: %w", name, err)
	}
	return &Container{ID: v.ID, Image: v.Image, Running: v.State.Running, Labels: v.Config.Labels}, nil
}

// RemoveContainer stops and removes a container. One that doesn't exist
// isn't an error.
func (c CLI) RemoveContainer(ctx context.Context, name string) error {
	_, stderr, err := c.run(ctx, "container", "rm", "--force", name)
	if err != nil && !notFound(stderr) {
		return fmt.Errorf("docker container rm %s: %w: %s", name, c.explain(err), strings.TrimSpace(stderr))
	}
	return nil
}

func (c CLI) run(ctx context.Context, args ...string) (stdout []byte, stderr string, err error) {
	var errb bytes.Buffer
	cmd := exec.CommandContext(ctx, c.command(), args...)
	cmd.Stderr = &errb
	out, err := cmd.Output()
	return out, errb.String(), err
}

// explain turns a missing docker binary into advice.
func (c CLI) explain(err error) error {
	if _, ok := err.(*exec.Error); ok { //nolint:errorlint // exec.Error is returned unwrapped
		return fmt.Errorf("the %s CLI isn't on PATH, and Solaria needs it here: %w", c.command(), err)
	}
	return err
}

func notFound(stderr string) bool {
	s := strings.ToLower(stderr)
	return strings.Contains(s, "no such image") || strings.Contains(s, "image not known") ||
		strings.Contains(s, "no such container") || strings.Contains(s, "no container with name")
}
