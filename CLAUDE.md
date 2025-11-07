# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Nunu-based Go application template following clean hexagonal architecture. Nunu is a scaffolding tool built on popular Go libraries. The project supports multiple binaries (HTTP server + scheduled tasks) with dependency injection via Google Wire.

## Common Commands

### Development Setup
```bash
# Install required tools (Wire, Mockgen, Swag)
make init

# Start services with Docker Compose and run server
make bootstrap

# Run server only (requires config/local.yml)
nunu run ./cmd/server
```

### Building
```bash
# Build server binary to ./bin/server
make build

# Build Docker image (for task binary example)
make docker
```

### Testing
```bash
# Generate mocks for testing
make mock

# Run tests with coverage (generates coverage.html)
make test
```

### Code Generation
```bash
# Generate Wire dependency injection code
cd cmd/server/wire && wire

# Generate Swagger documentation
make swag
# Docs available at http://localhost:8000/swagger/index.html
```

### Running Individual Tests
```bash
# Run specific test file
go test ./test/server/user_test.go -v

# Run with coverage for specific package
go test -cover ./internal/service/...
```

## Architecture

### Project Structure

The codebase follows clean architecture with clear separation of concerns:

- **cmd/**: Application entry points
  - `cmd/server/`: HTTP API server + in-process jobs
  - `cmd/task/`: Scheduled tasks (cron jobs)
  - Each has separate `wire/` directory for dependency injection

- **internal/**: Private application code (not importable by external projects)
  - `handler/`: HTTP request handlers (Gin controllers)
  - `service/`: Business logic layer with transaction management
  - `repository/`: Data access layer with multi-DB support
  - `router/`: Route definitions organized by domain
  - `model/`: Domain models and database entities
  - `middleware/`: HTTP middleware (auth, logging, CORS)
  - `job/`: In-process background jobs (runs in server binary)
  - `task/`: Scheduled task handlers (runs in task binary)
  - `server/`: Server implementations (HTTP, Job, Task)

- **pkg/**: Reusable packages (can be extracted as libraries)
  - `app/`: Application orchestrator for multi-server management
  - `server/`: Server interfaces and base implementations
  - `config/`: Viper-based configuration management
  - `log/`: Zap logger with Lumberjack rotation
  - `jwt/`: JWT token generation and validation
  - `sid/`: Distributed ID generation (Sonyflake + Base62)
  - `zapgorm2/`: GORM logging adapter

- **api/v1/**: API contracts (requests, responses, errors)
- **config/**: YAML configuration files (local.yml, prod.yml)

### Dependency Injection with Wire

The project uses Google Wire for compile-time dependency injection:

1. **Wire configuration**: Located in `cmd/{server,task}/wire/wire.go`
2. **Provider sets**: Dependencies organized into logical sets (repositorySet, serviceSet, handlerSet, serverSet)
3. **Generated code**: Running `wire` generates `wire_gen.go` (not committed to repo)
4. **Separate DI graphs**: Server and Task binaries have different dependency requirements

**Important**: After adding new constructors or modifying wire.go, you must regenerate:
```bash
cd cmd/server/wire && wire
```

### Request Flow

Standard HTTP request flow:
```
HTTP Request
  → Middleware (CORS, Logging)
  → Router (Gin)
  → Handler (validates input, calls service)
  → Service (business logic, transaction management)
  → Repository (database operations via GORM)
  → Database
```

### Adding New Features

To add a new domain (e.g., "product"):

1. **Define API contracts** in `api/v1/product.go`:
   - Request/response structs
   - Error codes

2. **Create model** in `internal/model/product.go`:
   - Database entity with GORM tags

3. **Implement repository** in `internal/repository/product.go`:
   - Define `ProductRepository` interface
   - Implement with methods using `r.DB(ctx)` for transaction support

4. **Implement service** in `internal/service/product.go`:
   - Define `ProductService` interface
   - Implement with embedded `*Service` for access to logger, sid, jwt, tm
   - Use `s.tm.Transaction()` for multi-step operations

5. **Implement handler** in `internal/handler/product.go`:
   - Validate requests
   - Call service methods
   - Use `handler.HandleSuccess()` or `handler.HandleError()` for responses

6. **Define routes** in `internal/router/product.go`:
   - Create `InitProductRouter(deps RouterDeps, r *gin.RouterGroup)`
   - Organize routes by auth level (noAuth, noStrictAuth, strictAuth)
   - Call from `NewHTTPServer()` in `internal/server/http.go`

7. **Update Wire configuration** in `cmd/server/wire/wire.go`:
   - Add constructor to appropriate set (repositorySet, serviceSet, handlerSet)
   - Add to `RouterDeps` struct if needed
   - Regenerate: `cd cmd/server/wire && wire`

### Database & Transactions

- **Multi-database support**: SQLite (default), MySQL, PostgreSQL, MongoDB, Redis
- **Connection configuration**: Set in `config/local.yml` under `data.db.user`
- **Schema migrations**: No dedicated migration tool. Use GORM's `AutoMigrate()` in application startup or external migration tools (e.g., golang-migrate, goose)
- **Transaction pattern**:
  ```go
  err = s.tm.Transaction(ctx, func(ctx context.Context) error {
      // All repository calls use ctx with embedded transaction
      if err := s.repo.Create(ctx, entity); err != nil {
          return err // Automatic rollback
      }
      return nil // Automatic commit
  })
  ```
- **Context-aware DB access**: Always use `r.DB(ctx)` in repositories to support transactions

### Middleware & Authentication

**Authentication middleware**:
- `middleware.StrictAuth()`: Requires valid JWT token, aborts if missing
- `middleware.NoStrictAuth()`: Optional token, populates context if present
- Token checked in: Authorization header, Cookie, Query parameter

**JWT Claims**:
```go
type MyCustomClaims struct {
    UserId string
    jwt.RegisteredClaims
}
```

**Getting user from context**:
```go
userId := ctx.GetString("userId") // Set by auth middleware
```

### Configuration Management

- **Config files**: `config/local.yml`, `config/prod.yml`
- **Environment override**: Set `APP_CONF` environment variable to specify config path
- **Command line**: Use `-conf` flag: `./server -conf config/prod.yml`
- **Access pattern**: Inject `*viper.Viper` and use `conf.GetString()`, `conf.GetInt()`, etc.
- **Important sections**:
  - `http.port`: Server port (default 8000)
  - `data.db.user.driver`: Database driver (sqlite, mysql, postgres)
  - `data.db.user.dsn`: Database connection string
  - `security.jwt.key`: JWT signing key
  - `log.*`: Logging configuration

### Server & Task Binaries

**Two deployment models**:

1. **Server binary** (`cmd/server/`):
   - HTTP API server (Gin on port 8000)
   - In-process job server (Kafka/message consumer example)
   - Both run concurrently managed by App orchestrator

2. **Task binary** (`cmd/task/`):
   - Runs scheduled/cron jobs using gocron
   - Separate process for independent scaling
   - Only includes repository and task layers (no handlers/services)

**Adding scheduled tasks**:
1. Implement task in `internal/task/yourtask.go`
2. Register in `internal/server/task.go` with cron expression
3. Update `cmd/task/wire/wire.go` to include new task
4. Deploy task binary separately

### Testing Patterns

- **Mocks**: Generated via mockgen (see Makefile)
- **Test location**: `test/server/` for integration tests
- **Coverage**: Tests cover handler, service, and repository layers
- **Test helpers**: Use `httpexpect` for HTTP testing, `sqlmock` for repository tests

### API Documentation

- **Swagger annotations**: Added to `cmd/server/main.go` and handlers
- **Generation**: `make swag` generates docs to `./docs`
- **Access**: http://localhost:8000/swagger/index.html after starting server
- **Format**: Use Swag comments like `// @Summary`, `// @Param`, `// @Success`

## Key Libraries

- **Gin**: HTTP framework
- **GORM**: ORM with SQLite/MySQL/PostgreSQL support
- **Wire**: Dependency injection (compile-time)
- **Viper**: Configuration management
- **Zap**: Structured logging
- **Lumberjack**: Log rotation
- **golang-jwt**: JWT authentication
- **Sonyflake**: Distributed unique ID generation
- **gocron**: Cron job scheduling
- **Swag**: Swagger documentation generation
- **gomock**: Mock generation for testing
