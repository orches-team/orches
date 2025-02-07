package utils

import (
	"fmt"
	"os/exec"
)

func ExecNoOutput(argv ...string) error {
	_, err := ExecOutput(argv...)
	return err
}

func ExecOutput(argv ...string) ([]byte, error) {
	cmd := argv[0]
	args := argv[1:]
	c := exec.Command(cmd, args...)

	// TODO: use Output?
	out, err := c.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("failed to execute command: %w\noutput:\n%s", err, string(out))
	}
	return out, nil
}
