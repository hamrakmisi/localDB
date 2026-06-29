package docker

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestSmoke exercises the full lifecycle against a real Docker engine.
// Skipped unless LOCALDB_SMOKE=1 so normal `go test` stays offline.
func TestSmoke(t *testing.T) {
	if os.Getenv("LOCALDB_SMOKE") != "1" {
		t.Skip("set LOCALDB_SMOKE=1 to run against a real Docker engine")
	}
	m, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := m.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	spec := Spec{
		Name: "smoke", Engine: MariaDB, Port: 33999,
		User: "dev", Password: "devpass", Database: "smoke",
	}
	// best-effort cleanup of a leftover from a prior run
	if ins, _ := m.List(ctx); ins != nil {
		for _, i := range ins {
			if i.Name == "smoke" {
				_ = m.Remove(ctx, i.ID, false)
			}
		}
	}

	if err := m.CreateAndStart(ctx, spec); err != nil {
		t.Fatal("create:", err)
	}

	ins, err := m.List(ctx)
	if err != nil {
		t.Fatal("list:", err)
	}
	var found *Instance
	for i := range ins {
		if ins[i].Name == "smoke" {
			found = &ins[i]
		}
	}
	if found == nil {
		t.Fatal("created instance not in list")
	}
	if found.State != "running" {
		t.Fatalf("expected running, got %q", found.State)
	}
	t.Logf("smoke instance up: %s %s port %s", found.Engine, found.State, found.Port)

	if err := m.Stop(ctx, found.ID); err != nil {
		t.Fatal("stop:", err)
	}
	if err := m.Remove(ctx, found.ID, false); err != nil {
		t.Fatal("remove:", err)
	}
}
