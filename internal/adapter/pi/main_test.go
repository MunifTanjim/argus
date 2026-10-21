package pi

import (
	"os"
	"testing"

	"github.com/MunifTanjim/argus/internal/config"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "argus-pi-cache-")
	if err != nil {
		panic(err)
	}
	config.CacheDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
