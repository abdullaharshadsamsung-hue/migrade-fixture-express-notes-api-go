# Notes API (Go)

A Notes API service, migrated from a JavaScript/Express codebase to Go using the standard library. Both modules from the original project have been migrated (2/2).

> **Migration status:** The build, the target unit tests and a behavior comparison against the original Express application all passed. The 96% confidence figure is the migration tool's own self-assessment, not a measured result. One module is flagged as low confidence and needs manual review. See [Manual review required](#manual-review-required).

---

## Tech stack

| Component        | Technology                                                 |
|------------------|------------------------------------------------------------|
| Language         | Go (see `go.mod` for the required version)                 |
| HTTP layer       | Go standard library (`net/http`)                           |
| Dependency mgmt  | Go modules (`go.mod`)                                      |
| Containerization | `Dockerfile`, `docker-compose.yml`                         |

---

## Prerequisites

- A Go toolchain that matches the version declared in `go.mod`.
- A reachable database, with its connection string available as `DATABASE_URL`. The migration did not detect the database engine or a schema setup step. Check `internal/config/config.go` and `internal/notes/store.go` to find out what is expected.
- Write access to `/app`. The detected install and run commands use absolute `/app` paths (see the note below).

---

## Getting started

Run these steps in order, from a fresh clone to a running server.

> **Note on paths:** The commands below were detected as written. They assume the project source is located at `/app` and they place the binary in `/app/bin`. This matches a container layout. If you work elsewhere, either place or symlink the project at `/app`, or adjust the paths to match your checkout.

### 1. Install dependencies and build

```sh
mkdir -p /app/bin && cd /app && go mod download && go build -o /app/bin/server ./cmd/server
```

### 2. Resolve module dependencies

This step was run during migration and succeeded. A fresh clone needs it too.

```sh
go mod tidy
```

If `go mod tidy` modifies `go.mod`, re-run the build command from step 1. This makes sure the binary at `/app/bin/server` reflects the tidied dependencies.

### 3. Configure environment variables

Set the variables listed in [Environment variables](#environment-variables):

```sh
export DATABASE_URL="<your database connection string>"
export PORT="<port to listen on>"
export JWT_SECRET="<a strong secret value>"
```

### 4. Database setup

No database setup or migration command was detected. Before starting the server, provision the database yourself and make sure it is reachable at `DATABASE_URL`. Review `internal/notes/store.go` to see which tables or structures the code expects.

### 5. Run the server

```sh
/app/bin/server
```

### Docker

The repository includes a `Dockerfile` and a `docker-compose.yml`. Their contents were not part of the migration verification. Review them before you rely on them, and confirm they use the same build and run commands shown above.

---

## Running tests

```sh
go test ./...
```

The target unit tests passed during migration.

---

## Environment variables

| Variable       | Required | Description                                                                                       |
|----------------|----------|---------------------------------------------------------------------------------------------------|
| `DATABASE_URL` | Yes      | Connection string for the database used to store notes.                                           |
| `PORT`         | Yes      | Port the HTTP server listens on.                                                                  |
| `JWT_SECRET`   | Yes      | Secret used for JWT signing and verification. Keep it out of version control.                     |

The descriptions above are inferred from the variable names. Check `internal/config/config.go` for the exact parsing, any default values, and the behavior when a variable is missing.

---

## Architecture overview

The descriptions below are inferred from the file layout. Confirm them against the source.

```
.
├── cmd/
│   └── server/
│       ├── main.go          # Entry point: loads config, wires dependencies, starts the server
│       └── router.go        # HTTP route registration (replaces the Express router)
├── internal/
│   ├── config/
│   │   └── config.go        # Reads DATABASE_URL, PORT and JWT_SECRET from the environment
│   ├── httpapi/
│   │   └── server.go        # HTTP server construction/handlers
│   └── notes/
│       └── store.go         # Notes data access / persistence
├── Dockerfile
├── docker-compose.yml
└── go.mod
```

- **`cmd/server`** builds into the `server` binary (`go build -o /app/bin/server ./cmd/server`).
- **`internal/`** packages can only be imported from within this module. Configuration, the HTTP layer and notes storage each live in their own package.
- Both `cmd/server/router.go` and `internal/httpapi/server.go` touch the HTTP layer. Check which file owns route definitions and which owns handlers or server setup before you add new endpoints.

---

## Migration notes

Changes from the original Express codebase:

- **Runtime and language:** Node.js/JavaScript was replaced with Go. The app is now a single compiled binary instead of an interpreted Node process.
- **HTTP framework:** Express was replaced with the Go standard library. Express routing and middleware are now implemented with `net/http` handlers, located in `cmd/server/router.go` and `internal/httpapi/server.go`.
- **Dependency management:** npm dependencies were replaced with Go modules (`go.mod`). Run `go mod download` and `go mod tidy` instead of `npm install`.
- **Configuration:** Environment-variable handling is centralized in `internal/config/config.go`.
- **Notes logic:** The notes functionality from the original application now lives in `internal/notes/store.go`.
- **Verification:** The build passed, the target unit tests passed, and a behavior comparison against the original Express app passed. The exact scope of that comparison (which endpoints and cases were covered) is not documented here. Treat it as evidence of parity, not proof of complete parity.

---

## Known limitations

- **No unmigratable components:** Every component was migrated, and no setup command failed during migration.
- **Hard-coded `/app` paths:** The install and run commands assume the project lives at `/app`. Local development outside that path requires adjusting the commands.
- **No database setup step:** No schema creation or migration command was detected. You must prepare the database manually before running the server.
- **Low-confidence module:** The original notes module was flagged as low confidence. Its Go equivalent should be reviewed even though the tests and behavior comparison passed.

---

## Manual review required

| Item | What to check |
|------|---------------|
| Notes module (`internal/notes/store.go`) | **Flagged low confidence.** Check queries, error handling, edge cases (empty or missing notes, invalid IDs, ownership checks) and response shapes against the original Express behavior. |
| `internal/config/config.go` | Confirm how missing or invalid values for `DATABASE_URL`, `PORT` and `JWT_SECRET` are handled, and whether any defaults exist. |
| JWT handling (`internal/httpapi/server.go`, `cmd/server/router.go`) | Check that token verification, the signing algorithm and the auth failure responses match the original app. |
| `go.mod` | Review any changes made by `go mod tidy` and commit them. |
| `Dockerfile`, `docker-compose.yml` | Confirm they build and run the binary the same way as the commands in this README, and that they supply all required environment variables. |