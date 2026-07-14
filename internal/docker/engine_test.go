package docker

import "testing"

func TestPostgresConfiguration(t *testing.T) {
	spec := Spec{Name: "app", Engine: Postgres, Port: 5432, User: "dev", Password: "secret", Database: "app"}

	if got, want := Postgres.Image(), "postgres:16"; got != want {
		t.Fatalf("Image() = %q, want %q", got, want)
	}
	if got, want := Postgres.DefaultPort(), 5432; got != want {
		t.Fatalf("DefaultPort() = %d, want %d", got, want)
	}
	if got, want := Postgres.dataDir(), "/var/lib/postgresql/data"; got != want {
		t.Fatalf("dataDir() = %q, want %q", got, want)
	}

	env := Postgres.environment(spec)
	want := map[string]bool{
		"POSTGRES_USER=dev":        true,
		"POSTGRES_PASSWORD=secret": true,
		"POSTGRES_DB=app":          true,
	}
	for _, value := range env {
		delete(want, value)
	}
	if len(want) != 0 {
		t.Fatalf("missing PostgreSQL environment values: %v", want)
	}
}

func TestPostgresPasswordlessConfigurationUsesTrust(t *testing.T) {
	env := Postgres.environment(Spec{Engine: Postgres})
	for _, value := range env {
		if value == "POSTGRES_HOST_AUTH_METHOD=trust" {
			return
		}
	}
	t.Fatal("passwordless PostgreSQL configuration must enable trust authentication")
}
