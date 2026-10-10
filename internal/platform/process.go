package platform

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Keep stderr separate: Windows PowerShell can emit #< CLIXML progress
// records there even when its JSON stdout and exit status are successful.
func captureOutput(cmd *exec.Cmd) ([]byte, error) {
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", cmd.Path, err, strings.TrimSpace(stderr.String()))
	}
	return bytes.TrimPrefix(out, []byte{0xef, 0xbb, 0xbf}), nil
}
