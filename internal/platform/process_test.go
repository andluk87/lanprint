package platform

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCommandHelper(t *testing.T) {
	mode := os.Getenv("LANPRINT_TEST_HELPER")
	if mode == "" {
		return
	}
	fmt.Fprint(os.Stderr, "#< CLIXML\n<Objs>progress</Objs>")
	if mode == "fail" {
		os.Exit(1)
	}
	fmt.Print("\ufeff[{\"name\":\"USB printer\"}]")
	os.Exit(0)
}

func TestCaptureSeparatesPowerShellProgressFromJSON(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCommandHelper$")
	cmd.Env = append(os.Environ(), "LANPRINT_TEST_HELPER=success")
	out, err := captureOutput(cmd)
	if err != nil {
		t.Fatal(err)
	}
	var printers []Printer
	if err := json.Unmarshal(out, &printers); err != nil {
		t.Fatal(err)
	}
	if len(printers) != 1 || printers[0].Name != "USB printer" {
		t.Fatalf("%s", out)
	}
}

func TestCapturePreservesFailure(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCommandHelper$")
	cmd.Env = append(os.Environ(), "LANPRINT_TEST_HELPER=fail")
	if _, err := captureOutput(cmd); err == nil || !strings.Contains(err.Error(), "CLIXML") {
		t.Fatalf("%v", err)
	}
}
