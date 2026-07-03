// Package docker wraps the minimal Docker engine operations localdb needs to
// run local MySQL / MariaDB containers for development.
package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
)

// Label applied to every container/volume localdb creates so we can list and
// manage only our own resources and never touch the user's other containers.
const (
	managedLabel = "localdb.managed"
	engineLabel  = "localdb.engine"
	portLabel    = "localdb.port"
	userLabel    = "localdb.user"
	dbLabel      = "localdb.database"

	namePrefix = "localdb-"
)

// Engine is the database flavor to run.
type Engine string

const (
	MySQL   Engine = "mysql"
	MariaDB Engine = "mariadb"
)

// Image returns the Docker image reference for the engine.
func (e Engine) Image() string {
	switch e {
	case MariaDB:
		return "mariadb:11"
	default:
		return "mysql:8.0"
	}
}

// serverArgs returns extra mysqld flags for the engine. MySQL 8 defaults to the
// caching_sha2_password auth plugin, which makes JDBC clients (DBeaver) fail with
// "Public Key Retrieval is not allowed" over non-TLS connections. Forcing
// mysql_native_password avoids that. MariaDB does not use caching_sha2_password,
// so it needs no flag (and the flag is invalid there).
// clientBin returns the mysql client binary name for the engine.
// MariaDB 11 dropped the mysql symlink; use mariadb/mariadb-dump instead.
func (e Engine) clientBin() string {
	if e == MariaDB {
		return "mariadb"
	}
	return "mysql"
}

func (e Engine) dumpBin() string {
	if e == MariaDB {
		return "mariadb-dump"
	}
	return "mysqldump"
}

func (e Engine) serverArgs() []string {
	switch e {
	case MariaDB:
		return nil
	default:
		return []string{"--default-authentication-plugin=mysql_native_password"}
	}
}

// Spec describes a database the user wants to create.
type Spec struct {
	Name     string // logical name, e.g. "myapp"
	Engine   Engine
	Port     int    // host port mapped to container 3306
	User     string // non-root app user
	Password string // password for User (also set as root password)
	Database string // initial database created on first boot
}

// Instance is a localdb-managed container as reported by Docker.
type Instance struct {
	ID       string
	Name     string // logical name (prefix stripped)
	Engine   Engine
	Port     string
	User     string
	Database string
	State    string // running, exited, ...
	Status   string // human-readable, e.g. "Up 2 minutes"
}

// Manager talks to the Docker engine.
type Manager struct {
	cli *dockerclient.Client
}

// New creates a Manager. It honors DOCKER_HOST when set; otherwise it probes
// the common local sockets (Colima, Docker Desktop, default) so the app works
// without the user exporting any environment variable.
func New() (*Manager, error) {
	if err := ensureColimaRunning(); err != nil {
		return nil, fmt.Errorf("start colima: %w", err)
	}

	opts := []dockerclient.Opt{
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	}
	if os.Getenv("DOCKER_HOST") == "" {
		if host := detectSocket(); host != "" {
			opts = append(opts, dockerclient.WithHost(host))
		}
	}
	cli, err := dockerclient.NewClientWithOpts(opts...)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}
	return &Manager{cli: cli}, nil
}

// detectSocket returns a unix:// host for the first Docker socket found, or ""
// to let the SDK use its default.
func detectSocket() string {
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".colima", "default", "docker.sock"),
		filepath.Join(home, ".colima", "docker.sock"),
		filepath.Join(home, ".docker", "run", "docker.sock"),
		"/var/run/docker.sock",
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return "unix://" + p
		}
	}
	return ""
}

// ensureColimaRunning starts colima on macOS if it's installed but not
// already running, so the user doesn't have to remember to start it after
// every reboot. No-op on other OSes or when colima isn't installed.
func ensureColimaRunning() error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	if _, err := exec.LookPath("colima"); err != nil {
		return nil
	}
	if err := exec.Command("colima", "status").Run(); err == nil {
		return nil // already running
	}
	fmt.Fprintln(os.Stderr, "localdb: colima not running, starting it...")
	start := exec.Command("colima", "start")
	start.Stdout = os.Stderr
	start.Stderr = os.Stderr
	return start.Run()
}

func (m *Manager) Close() error { return m.cli.Close() }

// Ping verifies the Docker engine is reachable.
func (m *Manager) Ping(ctx context.Context) error {
	_, err := m.cli.Ping(ctx)
	if err != nil {
		return fmt.Errorf("docker engine unreachable (is colima started?): %w", err)
	}
	return nil
}

func containerName(logical string) string { return namePrefix + logical }

func logicalName(containerName string) string {
	return strings.TrimPrefix(strings.TrimPrefix(containerName, "/"), namePrefix)
}

func volumeName(logical string) string { return namePrefix + logical + "-data" }

// List returns all localdb-managed containers.
func (m *Manager) List(ctx context.Context) ([]Instance, error) {
	f := filters.NewArgs()
	f.Add("label", managedLabel+"=true")
	cs, err := m.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: f})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	out := make([]Instance, 0, len(cs))
	for _, c := range cs {
		name := ""
		if len(c.Names) > 0 {
			name = logicalName(c.Names[0])
		}
		out = append(out, Instance{
			ID:       c.ID,
			Name:     name,
			Engine:   Engine(c.Labels[engineLabel]),
			Port:     c.Labels[portLabel],
			User:     c.Labels[userLabel],
			Database: c.Labels[dbLabel],
			State:    c.State,
			Status:   c.Status,
		})
	}
	return out, nil
}

// Create pulls the image if needed and creates a stopped container for spec.
// It returns an error if a localdb container of the same name already exists.
func (m *Manager) Create(ctx context.Context, spec Spec) error {
	if err := m.ensureImage(ctx, spec.Engine.Image()); err != nil {
		return err
	}

	port := nat.Port("3306/tcp")
	hostPort := fmt.Sprintf("%d", spec.Port)

	env := []string{
		"MYSQL_USER=" + spec.User,
		"MYSQL_DATABASE=" + spec.Database,
	}
	if spec.Password == "" {
		env = append(env, "MYSQL_ALLOW_EMPTY_PASSWORD=yes")
	} else {
		env = append(env,
			"MYSQL_ROOT_PASSWORD="+spec.Password,
			"MYSQL_PASSWORD="+spec.Password,
		)
	}

	cfg := &container.Config{
		Image: spec.Engine.Image(),
		Env:   env,
		Cmd:   spec.Engine.serverArgs(),
		ExposedPorts: nat.PortSet{port: struct{}{}},
		Labels: map[string]string{
			managedLabel: "true",
			engineLabel:  string(spec.Engine),
			portLabel:    hostPort,
			userLabel:    spec.User,
			dbLabel:      spec.Database,
		},
	}

	hostCfg := &container.HostConfig{
		PortBindings: nat.PortMap{
			port: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: hostPort}},
		},
		Mounts: []mount.Mount{{
			Type:   mount.TypeVolume,
			Source: volumeName(spec.Name),
			Target: "/var/lib/mysql",
		}},
		RestartPolicy: container.RestartPolicy{Name: "unless-stopped"},
	}

	_, err := m.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, containerName(spec.Name))
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}
	return nil
}

// Start starts an existing container by ID.
func (m *Manager) Start(ctx context.Context, id string) error {
	if err := m.cli.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
		return fmt.Errorf("start container: %w", err)
	}
	return nil
}

// Stop stops a running container by ID.
func (m *Manager) Stop(ctx context.Context, id string) error {
	timeout := 10
	if err := m.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop container: %w", err)
	}
	return nil
}

// Remove stops (if needed) and removes the container. The named data volume is
// removed too when keepData is false.
func (m *Manager) Remove(ctx context.Context, id string, keepData bool) error {
	// Resolve the logical name before removal so we know which volume to drop.
	logical := ""
	if info, err := m.cli.ContainerInspect(ctx, id); err == nil {
		logical = logicalName(info.Name)
	}

	if err := m.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("remove container: %w", err)
	}

	if !keepData && logical != "" {
		// Best-effort: volume may not exist (e.g. created elsewhere). Ignore
		// "not found"; surface anything else.
		if err := m.cli.VolumeRemove(ctx, volumeName(logical), true); err != nil &&
			!dockerclient.IsErrNotFound(err) {
			return fmt.Errorf("remove volume: %w", err)
		}
	}
	return nil
}

func (m *Manager) ensureImage(ctx context.Context, ref string) error {
	// Already present?
	_, _, err := m.cli.ImageInspectWithRaw(ctx, ref)
	if err == nil {
		return nil
	}
	rc, err := m.cli.ImagePull(ctx, ref, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", ref, err)
	}
	defer rc.Close()
	// Drain pull output so the pull completes before we return.
	_, _ = io.Copy(io.Discard, rc)
	return nil
}

// CreateAndStart is a convenience used by the TUI: create then start, with a
// short settle so the container state is observable on next List.
func (m *Manager) CreateAndStart(ctx context.Context, spec Spec) error {
	if err := m.Create(ctx, spec); err != nil {
		return err
	}
	if err := m.Start(ctx, containerName(spec.Name)); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	return nil
}

// containerPassword returns the MYSQL_ROOT_PASSWORD stored in the container env.
// Returns "" (no error) for passwordless containers (MYSQL_ALLOW_EMPTY_PASSWORD).
func (m *Manager) containerPassword(ctx context.Context, id string) (string, error) {
	info, err := m.cli.ContainerInspect(ctx, id)
	if err != nil {
		return "", err
	}
	for _, env := range info.Config.Env {
		if strings.HasPrefix(env, "MYSQL_ROOT_PASSWORD=") {
			return strings.TrimPrefix(env, "MYSQL_ROOT_PASSWORD="), nil
		}
	}
	return "", nil // passwordless container
}

// rootArgs returns the mysql/mysqldump auth flags for root.
func rootArgs(password string) []string {
	if password == "" {
		return []string{"-uroot"}
	}
	return []string{"-uroot", "-p" + password}
}

// withEnvPath wraps cmd so it runs via /usr/bin/env with an explicit PATH,
// guaranteeing mysql/mysqldump are found regardless of the container's default env.
func withEnvPath(cmd []string) []string {
	return append([]string{
		"/usr/bin/env",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}, cmd...)
}

// execCapture runs cmd inside the container and returns stdout bytes.
func (m *Manager) execCapture(ctx context.Context, id string, cmd []string) ([]byte, error) {
	ex, err := m.cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd:          withEnvPath(cmd),
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return nil, fmt.Errorf("exec create: %w", err)
	}
	resp, err := m.cli.ContainerExecAttach(ctx, ex.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, resp.Reader); err != nil {
		return nil, fmt.Errorf("exec read: %w", err)
	}
	info, err := m.cli.ContainerExecInspect(ctx, ex.ID)
	if err != nil {
		return nil, fmt.Errorf("exec inspect: %w", err)
	}
	if info.ExitCode != 0 {
		return nil, fmt.Errorf("exit %d: %s", info.ExitCode, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// execWithStdin runs cmd inside the container piping input to its stdin.
func (m *Manager) execWithStdin(ctx context.Context, id string, cmd []string, input io.Reader) error {
	ex, err := m.cli.ContainerExecCreate(ctx, id, container.ExecOptions{
		Cmd:          withEnvPath(cmd),
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return fmt.Errorf("exec create: %w", err)
	}
	resp, err := m.cli.ContainerExecAttach(ctx, ex.ID, container.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("exec attach: %w", err)
	}
	defer resp.Close()

	stdinDone := make(chan struct{})
	go func() {
		io.Copy(resp.Conn, input)
		resp.CloseWrite()
		close(stdinDone)
	}()

	var stderr bytes.Buffer
	stdcopy.StdCopy(io.Discard, &stderr, resp.Reader)
	// drain stdin goroutine; broken pipe here is expected if the process exited early
	<-stdinDone

	info, err := m.cli.ContainerExecInspect(ctx, ex.ID)
	if err != nil {
		return fmt.Errorf("exec inspect: %w", err)
	}
	if info.ExitCode != 0 {
		return fmt.Errorf("exit %d: %s", info.ExitCode, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// waitReady polls until the DB inside the container accepts a connection.
func (m *Manager) waitReady(ctx context.Context, id string, eng Engine, password string) error {
	client := eng.clientBin()
	for {
		_, err := m.execCapture(ctx, id, append(append([]string{client}, rootArgs(password)...), "-e", "SELECT 1"))
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for DB: %w", ctx.Err())
		case <-time.After(time.Second):
		}
	}
}

// Dump runs the dump tool inside the container and writes the result to destPath.
func (m *Manager) Dump(ctx context.Context, inst Instance, destPath string) error {
	pass, err := m.containerPassword(ctx, inst.ID)
	if err != nil {
		return err
	}
	data, err := m.execCapture(ctx, inst.ID, append(
		append([]string{inst.Engine.dumpBin()}, rootArgs(pass)...),
		"--databases", inst.Database,
	))
	if err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	return os.WriteFile(destPath, data, 0600)
}

// Restore pipes a SQL file into the DB client inside the container.
func (m *Manager) Restore(ctx context.Context, inst Instance, srcPath string) error {
	pass, err := m.containerPassword(ctx, inst.ID)
	if err != nil {
		return err
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	// Append database name so dumps without USE statements restore into the right db.
	cmd := append(append([]string{inst.Engine.clientBin()}, rootArgs(pass)...), inst.Database)
	return m.execWithStdin(ctx, inst.ID, cmd, f)
}

// Clone dumps src then creates a new container (newSpec) and restores the dump into it.
// src must be running.
func (m *Manager) Clone(ctx context.Context, src Instance, newSpec Spec) error {
	pass, err := m.containerPassword(ctx, src.ID)
	if err != nil {
		return err
	}
	data, err := m.execCapture(ctx, src.ID, append(
		append([]string{src.Engine.dumpBin()}, rootArgs(pass)...),
		"--databases", src.Database,
	))
	if err != nil {
		return fmt.Errorf("dump source: %w", err)
	}
	if newSpec.Password == "" {
		newSpec.Password = pass
	}
	if err := m.CreateAndStart(ctx, newSpec); err != nil {
		return err
	}
	newCtr := containerName(newSpec.Name)
	newPass, err := m.containerPassword(ctx, newCtr)
	if err != nil {
		return err
	}
	readyCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := m.waitReady(readyCtx, newCtr, newSpec.Engine, newPass); err != nil {
		return fmt.Errorf("new container not ready: %w", err)
	}
	if err := m.execWithStdin(ctx, newCtr, append([]string{newSpec.Engine.clientBin()}, rootArgs(newPass)...), bytes.NewReader(data)); err != nil {
		return fmt.Errorf("restore into clone: %w", err)
	}
	return nil
}

// Recreate stops, removes (keepData=true), and recreates the container with newSpec.
// The logical name must be unchanged so the existing volume is reused.
// If credentials changed, ALTER USER is applied after the new container is ready.
func (m *Manager) Recreate(ctx context.Context, inst Instance, newSpec Spec) error {
	oldPass, _ := m.containerPassword(ctx, inst.ID)
	oldUser := inst.User
	wasRunning := inst.State == "running"

	if err := m.Remove(ctx, inst.ID, true); err != nil {
		return fmt.Errorf("remove old container: %w", err)
	}
	if err := m.Create(ctx, newSpec); err != nil {
		return err
	}
	if !wasRunning {
		return nil
	}
	if err := m.Start(ctx, containerName(newSpec.Name)); err != nil {
		return err
	}

	passwordChanged := oldPass != "" && newSpec.Password != oldPass
	userChanged := oldUser != "" && newSpec.User != oldUser
	if !passwordChanged && !userChanged {
		time.Sleep(300 * time.Millisecond)
		return nil
	}

	newCtr := containerName(newSpec.Name)
	readyCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := m.waitReady(readyCtx, newCtr, newSpec.Engine, oldPass); err != nil {
		return fmt.Errorf("container not ready: %w", err)
	}

	var sqlParts []string
	if userChanged {
		sqlParts = append(sqlParts, fmt.Sprintf(
			"RENAME USER '%s'@'%%' TO '%s'@'%%'", oldUser, newSpec.User,
		))
	}
	sqlParts = append(sqlParts,
		fmt.Sprintf("ALTER USER '%s'@'%%' IDENTIFIED WITH mysql_native_password BY '%s'", newSpec.User, newSpec.Password),
		fmt.Sprintf("ALTER USER 'root'@'%%' IDENTIFIED WITH mysql_native_password BY '%s'", newSpec.Password),
		"FLUSH PRIVILEGES",
	)
	sql := strings.Join(sqlParts, "; ")
	if _, err := m.execCapture(ctx, newCtr, append(append([]string{newSpec.Engine.clientBin()}, rootArgs(oldPass)...), "-e", sql)); err != nil {
		return fmt.Errorf("apply credential changes: %w", err)
	}
	return nil
}
