package utils

import (
	"fmt"
	"os"
	"os/exec"
)

// execCommand is the base function that handles command execution
func execCommand(env []string, argv ...string) ([]byte, error) {
	if len(argv) == 0 {
		return nil, fmt.Errorf("no command provided")
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("failed to execute command: %w\noutput:\n%s", err, string(out))
	}
	return out, nil
}

func ExecNoOutput(argv ...string) error {
	_, err := execCommand(nil, argv...)
	return err
}

func ExecOutput(argv ...string) ([]byte, error) {
	return execCommand(nil, argv...)
}

// ExecOutputEnv executes a command with additional environment variables and returns its output
func ExecOutputEnv(env []string, argv ...string) ([]byte, error) {
	return execCommand(env, argv...)
}

// ExecNoOutputEnv executes a command with additional environment variables
func ExecNoOutputEnv(env []string, argv ...string) error {
	_, err := execCommand(env, argv...)
	return err
}
