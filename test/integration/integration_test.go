package integration_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/orches-team/orches/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var cid string

const (
	containerConfigDir = "/etc/containers/systemd"
	systemdConfigDir   = "/etc/systemd/system"
)

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

func cmd(args ...string) []string {
	return append([]string{"podman", "exec", cid}, args...)
}

func run(t *testing.T, args ...string) []byte {
	out, err := runUnchecked(args...)
	require.NoError(t, err)
	return out
}

func runUnchecked(args ...string) ([]byte, error) {
	return utils.ExecOutput(cmd(args...)...)
}

func runOrches(t *testing.T, args ...string) []byte {
	args = append([]string{"/app/orches", "-vv"}, args...)
	out, err := runUnchecked(args...)
	if err != nil {
		// If the command failed, get recent journalctl output with higher priority logs
		journalOut, journalErr := runUnchecked("journalctl", "--no-pager", "-n", "100", "--priority=info..emerg")
		if journalErr == nil {
			t.Logf("\nRecent journalctl output:\n%s", string(journalOut))
		} else {
			t.Logf("failed to get journalctl output: %v", journalErr)
		}
		require.NoError(t, err)
	}
	return out
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
const testdir2 = "/orchestest2"

func cleanup(t *testing.T) {
	// ADD ALL UNITS USED IN TESTS HERE
	for _, unit := range []string{
		"caddy", "caddy2", "orches",
		"test", "test-network", "test-volume", "test-pod",
		"test.socket", "test.mount", "test.timer",
	} {
		runUnchecked("systemctl", "stop", unit)
	}

	run(t, "rm", "-rf", testdir)
	run(t, "rm", "-rf", testdir2)
	run(t, "rm", "-rf", "/etc/containers/systemd")
	run(t, "rm", "-rf", "/var/lib/orches")
}

func commit(t *testing.T, dir string) {
	run(t, "git", "-C", dir, "add", ".")
	run(t, "git", "-C", dir, "commit", "-m", "commit")
}

func addAndCommit(t *testing.T, path, content string) {
	addFile(t, path, content)
	commit(t, filepath.Dir(path))
}

func removeAndCommit(t *testing.T, path string) {
	run(t, "rm", path)
	commit(t, filepath.Dir(path))
}

func TestOrches(t *testing.T) {
	defer cleanup(t)

	run(t, "mkdir", "-p", testdir)
	run(t, "git", "-C", testdir, "init")

	// Init with caddy on 8080
	addAndCommit(t, filepath.Join(testdir, "caddy.container"), `[Container]
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
	addAndCommit(t, filepath.Join(testdir, "caddy.container"), `[Container]
Image=docker.io/library/caddy:alpine
PublishPort=9090:80
`)

	runOrches(t, "sync")

	out = run(t, "systemctl", "status", "caddy")
	assert.Contains(t, string(out), "Active: active (running)")

	out = run(t, "curl", "-s", "http://localhost:9090")
	assert.Contains(t, string(out), "Caddy")

	// Drop caddy, and spawn it again as a different container on 8888
	removeAndCommit(t, filepath.Join(testdir, "caddy.container"))
	addAndCommit(t, filepath.Join(testdir, "caddy2.container"), `[Container]
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

func TestOrchesSelfUpdate(t *testing.T) {
	defer cleanup(t)

	run(t, "mkdir", "-p", testdir)
	run(t, "git", "-C", testdir, "init")

	// Let's mock orches with caddy
	addAndCommit(t, filepath.Join(testdir, "orches.container"), `[Container]
Image=docker.io/library/caddy:alpine
PublishPort=8080:80
`)

	runOrches(t, "init", testdir)

	out := run(t, "systemctl", "status", "orches")
	assert.Contains(t, string(out), "Active: active (running)")

	// Start the run process
	syncCmd := cmd("/app/orches", "-vv", "run", "--interval", "1")
	cmd := exec.Command(syncCmd[0], syncCmd[1:]...)
	require.NoError(t, cmd.Start())

	// Fake an update
	addAndCommit(t, filepath.Join(testdir, "orches.container"), `[Container]
Image=docker.io/library/caddy:alpine
PublishPort=9090:80
`)

	// Wait for the sync for a bit
	time.Sleep(2 * time.Second)

	// Now let's verify the faked update
	// The process itself should have died
	require.NoError(t, cmd.Wait())

	// The service should still be running (because orches doesn't stop itself)
	out = run(t, "systemctl", "status", "orches")
	assert.Contains(t, string(out), "Active: active (running)")

	// But the service file should have been updated
	out = run(t, "cat", "/etc/containers/systemd/orches.container")
	assert.Contains(t, string(out), "9090:80")
}

func TestOrchesSwitchRepo(t *testing.T) {
	defer cleanup(t)

	// Create first repo
	run(t, "mkdir", "-p", testdir)
	run(t, "git", "-C", testdir, "init")

	// Add initial caddy container on 8080
	addAndCommit(t, filepath.Join(testdir, "caddy.container"), `[Container]
Image=docker.io/library/caddy:alpine
PublishPort=8080:80
`)

	runOrches(t, "init", testdir)

	// Verify initial state
	out := run(t, "systemctl", "status", "caddy")
	assert.Contains(t, string(out), "Active: active (running)")
	out = run(t, "curl", "-s", "http://localhost:8080")
	assert.Contains(t, string(out), "Caddy")

	// Start the run process
	syncCmd := cmd("/app/orches", "-vv", "run", "--interval", "10")
	cmd := exec.Command(syncCmd[0], syncCmd[1:]...)
	require.NoError(t, cmd.Start())

	// Give the daemon time to start
	time.Sleep(2 * time.Second)

	// Create second repo
	run(t, "mkdir", "-p", testdir2)
	run(t, "git", "-C", testdir2, "init")

	// Add different caddy config in new repo
	addAndCommit(t, filepath.Join(testdir2, "caddy.container"), `[Container]
Image=docker.io/library/caddy:alpine
PublishPort=9090:80
`)

	// Switch to new repo
	runOrches(t, "switch", testdir2)

	// Give the daemon time to exit
	time.Sleep(1 * time.Second)

	// Verify the daemon process exited
	err := cmd.Wait()
	assert.NoError(t, err, "orches process should exit cleanly after switch")

	// Verify the switch worked
	out = run(t, "systemctl", "status", "caddy")
	assert.Contains(t, string(out), "Active: active (running)")

	// Old port should not work
	_, err = runUnchecked("curl", "-s", "http://localhost:8080")
	assert.Error(t, err)

	// New port should work
	out = run(t, "curl", "-s", "http://localhost:9090")
	assert.Contains(t, string(out), "Caddy")

	// Verify repo status shows new path
	out = runOrches(t, "status")
	assert.Contains(t, string(out), testdir2)
}

func TestOrchesRun(t *testing.T) {
	defer cleanup(t)

	// Create initial repo
	run(t, "mkdir", "-p", testdir)
	run(t, "git", "-C", testdir, "init")

	// Add initial caddy container on 8080
	addAndCommit(t, filepath.Join(testdir, "caddy.container"), `[Container]
Image=docker.io/library/caddy:alpine
PublishPort=8080:80
`)

	runOrches(t, "init", testdir)

	// Verify initial state
	out := run(t, "systemctl", "status", "caddy")
	assert.Contains(t, string(out), "Active: active (running)")
	out = run(t, "curl", "-s", "http://localhost:8080")
	assert.Contains(t, string(out), "Caddy")

	// Start the run process
	syncCmd := cmd("/app/orches", "-vv", "run", "--interval", "10")
	cmd := exec.Command(syncCmd[0], syncCmd[1:]...)
	require.NoError(t, cmd.Start())

	// Give the daemon time to start
	time.Sleep(2 * time.Second)

	// Update caddy to use port 9090
	addAndCommit(t, filepath.Join(testdir, "caddy.container"), `[Container]
Image=docker.io/library/caddy:alpine
PublishPort=9090:80
`)

	// Send sync command to daemon
	runOrches(t, "sync")

	// Give it time to process
	time.Sleep(2 * time.Second)

	// Verify the update was applied
	out = run(t, "systemctl", "status", "caddy")
	assert.Contains(t, string(out), "Active: active (running)")

	// Old port should not work
	_, err := runUnchecked("curl", "-s", "http://localhost:8080")
	assert.Error(t, err)

	// New port should work
	out = run(t, "curl", "-s", "http://localhost:9090")
	assert.Contains(t, string(out), "Caddy")

	// Send prune command to daemon
	runOrches(t, "prune")

	// Give it time to process
	time.Sleep(2 * time.Second)

	// Verify prune worked
	out, err = runUnchecked("systemctl", "status", "caddy")
	assert.Error(t, err)
	assert.Contains(t, string(out), "Unit caddy.service could not be found.")

	_, err = runUnchecked("ls", "/etc/containers/systemd/caddy.container")
	assert.Error(t, err)

	_, err = runUnchecked("ls", "/var/lib/orches/repo")
	assert.Error(t, err)

	// Give orches time to exit
	time.Sleep(1 * time.Second)

	// Verify orches process exited after prune
	err = cmd.Wait()
	assert.NoError(t, err, "orches process should exit cleanly after prune")
}

func TestOrchesSyncAllUnitTypes(t *testing.T) {
	tests := []struct {
		orchesUnit        string // The unit filename in orches
		content           string // The unit content
		associatedUnit    string // optional associated unit
		associatedContent string // content for the associated unit
		systemdName       string // The expected systemd unit name
		configDir         string // Expected config directory
	}{
		{
			orchesUnit: "test.container",
			content: `[Container]
Image=docker.io/library/alpine:latest
Exec=sleep 1`,
			systemdName: "test.service",
			configDir:   containerConfigDir,
		},
		{
			orchesUnit:  "test.network",
			content:     `[Network]`,
			systemdName: "test-network.service",
			configDir:   containerConfigDir,
		},
		{
			orchesUnit:  "test.volume",
			content:     `[Volume]`,
			systemdName: "test-volume.service",
			configDir:   containerConfigDir,
		},
		{
			orchesUnit:  "test.pod",
			content:     `[Pod]`,
			systemdName: "test-pod.service",
			configDir:   containerConfigDir,
		},
		{
			orchesUnit: "test.service",
			content: `[Service]
ExecStart=/bin/sleep 1`,
			systemdName: "test.service",
			configDir:   systemdConfigDir,
		},
		{
			orchesUnit: "test.socket",
			content: `[Socket]
ListenStream=127.0.0.1:9999`,
			associatedUnit: "test.service",
			associatedContent: `[Service]
ExecStart=/bin/sh -c "/bin/echo hello from socket"`,
			systemdName: "test.socket",
			configDir:   systemdConfigDir,
		},
		{
			orchesUnit: "mnt-test.mount",
			content: `[Mount]
What=tmpfs
Where=/mnt/test
Type=tmpfs`,
			systemdName: "mnt-test.mount",
			configDir:   systemdConfigDir,
		},
		{
			orchesUnit: "test.timer",
			content: `[Timer]
OnBootSec=1min

[Install]
WantedBy=timers.target`,
			associatedUnit:    "test.service",
			associatedContent: "[Service]\nExecStart=/bin/true\n",
			systemdName:       "test.timer",
			configDir:         systemdConfigDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.orchesUnit, func(t *testing.T) {
			defer cleanup(t)

			// Setup git repo
			run(t, "mkdir", "-p", testdir)
			run(t, "git", "-C", testdir, "init")

			// Add the unit
			addAndCommit(t, filepath.Join(testdir, tt.orchesUnit), tt.content)
			if tt.associatedUnit != "" {
				addAndCommit(t, filepath.Join(testdir, tt.associatedUnit), tt.associatedContent)
			}
			runOrches(t, "init", testdir)

			// Verify unit is properly synced
			run(t, "ls", filepath.Join(tt.configDir, tt.orchesUnit))
			if tt.associatedUnit != "" {
				run(t, "ls", filepath.Join(tt.configDir, tt.associatedUnit))
			}
			run(t, "systemctl", "status", tt.systemdName)

			runOrches(t, "prune")

			// Verify unit is gone
			_, err := runUnchecked("ls", filepath.Join(tt.configDir, tt.orchesUnit))
			assert.Error(t, err)
			if tt.associatedUnit != "" {
				_, err = runUnchecked("ls", filepath.Join(tt.configDir, tt.associatedUnit))
				assert.Error(t, err)
			}
			_, err = runUnchecked("systemctl", "status", tt.systemdName)
			assert.Error(t, err)
		})
	}
}
