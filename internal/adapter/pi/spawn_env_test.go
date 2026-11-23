package pi

import (
	"context"
	"slices"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
	"github.com/MunifTanjim/argus/internal/registry"
)

func TestSpawnPassesEnv(t *testing.T) {
	d := newDiscoverer(registry.New(), nil)
	f := &fakePi{t: t}
	stubSpawn(t, f)
	var env []string
	stubbed := spawnProc
	spawnProc = func(ctx context.Context, cwd, id, gate, model string) (*rpcConn, error) {
		env = spawnEnvFrom(ctx)
		return stubbed(ctx, cwd, id, gate, model)
	}
	if _, err := d.spawnSession(context.Background(), "/w", adapter.SpawnOptions{Env: []string{"GH_TOKEN=x"}}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(env, []string{"GH_TOKEN=x"}) {
		t.Errorf("env = %v", env)
	}
}
