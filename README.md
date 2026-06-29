# localdb

A tiny terminal UI to run local **MySQL** / **MariaDB** databases in Docker for
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
| `q` | quit |

**New-database form**

| Key | Action |
|-----|--------|
| tab / ↑↓ | next / previous field |
| ←/→ | toggle MySQL ↔ MariaDB |
| enter | create & start |
| esc | cancel |

## What it does under the hood

- Creates a container named `localdb-<name>` from `mysql:8` or `mariadb:11`.
- Binds the chosen host port to container port `3306` on `127.0.0.1`.
- Sets `MYSQL_ROOT_PASSWORD`, `MYSQL_USER`, `MYSQL_PASSWORD`, `MYSQL_DATABASE`.
- Mounts a named volume `localdb-<name>-data` at `/var/lib/mysql` so data
  survives restarts.
- Labels everything `localdb.managed=true` — it only ever lists or touches its
  own containers.

Connect with any client:

```sh
mysql -h 127.0.0.1 -P <port> -u <user> -p <database>
```
