package integration_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/orches-team/orches/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var cid string

func TestMain(m *testing.M) {
	code := 1
	defer func() { os.Exit(code) }()

	tmpDir, err := os.MkdirTemp("", "orches-test-")
	if err != nil {
		fmt.Printf("failed to create orches temp dir: %v", err)
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	err = utils.ExecNoOutput("go", "build", "-o", tmpDir+"/orches", "../../cmd/orches")
	if err != nil {
		fmt.Printf("failed to build orches: %v", err)
		panic(err)
	}

	err = utils.ExecNoOutput("podman", "build", "-t", "orches-testbase", "./container")
	if err != nil {
		fmt.Printf("failed to build orches-testbase: %v", err)
		panic(err)
	}

	c, err := utils.ExecOutput("podman", "run", "--quiet", "--rm", "-d", "-v", tmpDir+":/app:Z", "--privileged", "orches-testbase")
	if err != nil {
		fmt.Printf("failed to run orches-testbase: %v", err)
		panic(err)
	}
	cid = strings.TrimSpace(string(c))

	defer func() {
		err := utils.ExecNoOutput("podman", "stop", cid)
		if err != nil {
			utils.ExecNoOutput("podman", "kill", cid)
		}
	}()

	code = m.Run()
}

func run(t *testing.T, args ...string) []byte {
	out, err := runUnchecked(args...)
	require.NoError(t, err)
	return out
}

func runUnchecked(args ...string) ([]byte, error) {
	args = append([]string{"podman", "exec", cid}, args...)
	return utils.ExecOutput(args...)
}

func runOrches(t *testing.T, args ...string) []byte {
	args = append([]string{"/app/orches", "-vv"}, args...)
	return run(t, args...)
}

func addFile(t *testing.T, path, content string) {
	cmd := exec.Command("podman", "exec", "-i", cid, "tee", path)
	cmd.Stdin = strings.NewReader(content)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Log(string(out))
		t.FailNow()
	}
}

func TestAux(t *testing.T) {
	output := runOrches(t, "help")

	assert.Contains(t, string(output), "orches")
	assert.Contains(t, string(output), "Usage:")
	assert.Contains(t, string(output), "sync")
	assert.Contains(t, string(output), "switch")

	output = runOrches(t, "version")
	assert.Contains(t, string(output), "gitref")
	assert.Contains(t, string(output), "buildtime")
}

func TestSmokePodman(t *testing.T) {
	output := run(t, "podman", "run", "--rm", "--quiet", "alpine", "echo", "hello")

	assert.Contains(t, string(output), "hello")
}

const testdir = "/orchestest"

func cleanup(t *testing.T) {
	// ADD ALL UNITS USED IN TESTS HERE
	for _, unit := range []string{"caddy", "caddy2"} {
		runUnchecked("systemctl", "stop", unit)
	}

	run(t, "rm", "-rf", testdir)
	run(t, "rm", "-rf", "/etc/containers/systemd")
}

func commit(t *testing.T) {
	run(t, "git", "-C", testdir, "add", ".")
	run(t, "git", "-C", testdir, "commit", "-m", "commit")
}

func addOrchesFile(t *testing.T, path, content string) {
	addFile(t, path, content)
	commit(t)
}

func deleteOrchesFile(t *testing.T, path string) {
	run(t, "rm", path)
	commit(t)
}

func TestOrches(t *testing.T) {
	defer cleanup(t)

	run(t, "mkdir", "-p", testdir)
	run(t, "git", "-C", testdir, "init")

	// Init with caddy on 8080
	addOrchesFile(t, "/orchestest/caddy.container", `[Container]
Image=docker.io/library/caddy:alpine
PublishPort=8080:80
`)

	runOrches(t, "init", testdir)

	run(t, "ls", "/etc/containers/systemd/caddy.container")

	out := run(t, "systemctl", "status", "caddy")
	assert.Contains(t, string(out), "Active: active (running)")

	out = run(t, "curl", "-s", "http://localhost:8080")
	assert.Contains(t, string(out), "Caddy")

	// Move caddy to 9090
	addOrchesFile(t, "/orchestest/caddy.container", `[Container]
Image=docker.io/library/caddy:alpine
PublishPort=9090:80
`)

	runOrches(t, "sync")

	out = run(t, "systemctl", "status", "caddy")
	assert.Contains(t, string(out), "Active: active (running)")

	out = run(t, "curl", "-s", "http://localhost:9090")
	assert.Contains(t, string(out), "Caddy")

	// Drop caddy, and spawn it again as a different container on 8888
	deleteOrchesFile(t, "/orchestest/caddy.container")
	addOrchesFile(t, "/orchestest/caddy2.container", `[Container]
Image=docker.io/library/caddy:alpine
PublishPort=8888:80
`)

	runOrches(t, "sync")

	out, err := runUnchecked("systemctl", "status", "caddy")
	assert.Error(t, err)
	assert.Contains(t, string(out), "Unit caddy.service could not be found.")

	_, err = runUnchecked("curl", "-s", "http://localhost:9090")
	assert.Error(t, err)

	out = run(t, "systemctl", "status", "caddy2")
	assert.Contains(t, string(out), "Active: active (running)")

	out = run(t, "curl", "-s", "http://localhost:8888")
	assert.Contains(t, string(out), "Caddy")

	// Prune
	runOrches(t, "prune")

	out, err = runUnchecked("systemctl", "status", "caddy")
	assert.Error(t, err)
	assert.Contains(t, string(out), "Unit caddy.service could not be found.")

	out, err = runUnchecked("systemctl", "status", "caddy2")
	assert.Error(t, err)
	assert.Contains(t, string(out), "Unit caddy2.service could not be found.")

	_, err = runUnchecked("ls", "/etc/containers/systemd/caddy.container")
	assert.Error(t, err)

	_, err = runUnchecked("ls", "/var/lib/orches/repo")
	assert.Error(t, err)

}
