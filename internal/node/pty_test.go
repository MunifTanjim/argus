package node

import (
	"os/exec"
	"strings"
	"testing"
)

func TestStartPTYEcho(t *testing.T) {
	cmd := exec.Command("sh", "-c", "printf hello")
	f, err := startPTY(cmd, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 64)
	n, _ := f.Read(buf)
	if !strings.Contains(string(buf[:n]), "hello") {
		t.Fatalf("got %q", buf[:n])
	}
	_ = cmd.Wait()
}
