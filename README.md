# localdb

A tiny terminal UI to run local **MySQL**, **MariaDB**, and **PostgreSQL** databases in Docker for
development. Pick a name, port, user, and password — it spins up a container
with a persistent named volume. That's it.

## Requirements

- A running Docker engine. On macOS, [Colima](https://github.com/abiosoft/colima)
  works well:
  ```sh
  brew install colima docker
  colima start
  ```
- Go 1.23+ to build (`brew install go`).

`localdb` talks to the engine via the standard Docker environment
(`DOCKER_HOST` / the default socket), so anything `docker` itself can reach works.

## Build & run

```sh
go build -o localdb .
./localdb
```

## Keys

**List screen**

| Key | Action |
|-----|--------|
| ↑/↓ or j/k | move selection |
| `n` | new database |
| `s` | start / stop selected |
| `d` | delete selected (container + data volume) |
| `r` | refresh |
| `p` | copy selected database connection URI |
| `l` | view the selected container's latest logs |
| `q` | quit |

**New-database form**

| Key | Action |
|-----|--------|
| tab / ↑↓ | next / previous field |
| ←/→ | choose MySQL, MariaDB, or PostgreSQL |
| enter | create & start |
| esc | cancel |

## What it does under the hood

- Creates a container named `localdb-<name>` from `mysql:8`, `mariadb:11`, or `postgres:16`.
- Binds the chosen host port to the engine's standard port (`3306` for MySQL/MariaDB, `5432` for PostgreSQL) on `127.0.0.1`.
- Configures the matching MySQL/MariaDB or PostgreSQL initialization variables.
- Mounts a named volume `localdb-<name>-data` at the engine's data directory so data
  survives restarts.
- Labels everything `localdb.managed=true` — it only ever lists or touches its
  own containers.
- Shows whether each running database is actually ready to accept connections.

Connect with any client:

```sh
mysql -h 127.0.0.1 -P <port> -u <user> -p <database>
```

For PostgreSQL:

```sh
psql "postgresql://<user>@127.0.0.1:<port>/<database>"
```
