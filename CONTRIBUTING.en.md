<div align="right">Language: <a href="CONTRIBUTING.md">中文</a> | English</div>

# Contributing Guide

Thank you for your interest in the Hexagon project! We welcome all forms of contribution.

## Code of Conduct

Please be kind and respectful. We are committed to creating an open, inclusive community.

## How to Contribute

### Reporting Bugs

1. Make sure the bug hasn't already been reported — search [Issues](https://github.com/hexagon-codes/hexagon/issues)
2. If you can't find a related Issue, create a new one
3. Use a clear title and a detailed description
4. Provide reproduction steps, expected behavior, and actual behavior
5. Attach relevant code snippets or error logs

### Suggesting Features

1. Search existing Issues to make sure there are no duplicates
2. Create a new Issue with a `[Feature]` prefix
3. Clearly describe the feature requirement and use case
4. If possible, provide implementation ideas

### Submitting Code

#### Preparation

1. Install the Go version declared in `go.mod` or newer (the current minimum is Go 1.25.12)
2. Fork the repository
3. Clone it locally:
   ```bash
   git clone https://github.com/YOUR_USERNAME/hexagon.git
   cd hexagon
   ```
4. Add the upstream remote:
   ```bash
   git remote add upstream https://github.com/hexagon-codes/hexagon.git
   ```
5. Create a branch:
   ```bash
   git checkout -b feature/your-feature-name
   ```

#### Development Workflow

1. Run the required gates used by the `CI / Test` job:
   ```bash
   GOWORK=off go mod tidy -diff
   GOWORK=off go test -count=1 -race ./...
   ```

2. Run optional local development checks as needed. These commands are useful locally but are not current CI gates:
   ```bash
   make fmt      # format code
   make lint     # static analysis (requires golangci-lint locally)
   ```

3. Write tests to cover your changes

4. Commit your code:
   ```bash
   git add .
   git commit -m "feat: add your feature description"
   ```

5. Push to your fork:
   ```bash
   git push origin feature/your-feature-name
   ```

6. Create a Pull Request

#### Commit Message Convention

Use the [Conventional Commits](https://www.conventionalcommits.org/) specification:

- `feat`: new feature
- `fix`: bug fix
- `docs`: documentation update
- `style`: code formatting
- `refactor`: refactoring
- `test`: test-related changes
- `chore`: build or tooling changes

Examples:
```
feat(agent): add ReAct agent implementation
fix(llm): handle rate limit errors
docs: update README with quick start guide
```

## Code Standards

### Go Code Style

- Follow [Effective Go](https://go.dev/doc/effective_go)
- Format code with `gofmt`
- Use `golangci-lint` for static analysis
- Keep functions short (ideally under 50 lines)
- Use clear variable names and avoid unnecessary abbreviations

### Interface Design

- Interfaces should be small and focused
- Prefer composition over inheritance
- Use `context.Context` as the first parameter
- Wrap errors with `fmt.Errorf("...: %w", err)`

### Documentation

- All exported functions and types must have doc comments
- Doc comments should start with the name of the entity being documented
- Provide usage examples (Example functions)

### Testing

- Cover normal, error, and boundary behavior affected by the change
- Use table-driven tests
- Mock external dependencies
- Test file naming: `xxx_test.go`

## Project Structure

```
hexagon/
├── agent/         # Agent core (incl. a2a / artifact / semantic / skill)
├── core/          # Core interfaces (Component / Runnable / Stream)
├── orchestration/ # Orchestration (graph / chain / workflow / planner)
├── rag/           # RAG system (incl. adw, etc.)
├── runtime/       # Unified execution runtime (middleware / strategy)
├── observe/       # Observability (tracer / metrics / otel / devui)
├── security/      # Security (guard / rbac / cost / audit, etc.)
├── tool/          # Tool system
├── store/         # Storage (vector/*)
├── internal/      # Internal implementation (not exposed externally)
├── examples/      # Example code (separate module)
├── testing/       # Test helpers and integration tests
└── docs/          # Documentation
```

> Note: this project does not use a `pkg/` directory, following mainstream Go community practice.

## Pull Request Process

1. PR title should follow Conventional Commits format
2. Fill in all required fields in the PR template
3. Ensure the `CI / Test` check passes
4. Wait for code review
5. Make changes based on feedback
6. Delete your branch after merging

## Release Process

Version numbers follow [Semantic Versioning](https://semver.org/):

- `MAJOR.MINOR.PATCH`
- MAJOR: incompatible API changes
- MINOR: backward-compatible new features
- PATCH: backward-compatible bug fixes

A Git tag matching `v*` triggers the release workflow:

1. `Release / Validate` runs `go mod tidy -diff` and the full race-enabled test suite
2. After validation passes, `Release / Publish` creates the GitHub Release and generates release notes
3. A tag containing `-` (for example, `v0.6.0-rc.1`) is published as a prerelease

See [COMPATIBILITY.md](COMPATIBILITY.md) for compatibility and BREAKING-change rules during v0.x.

## Getting Help

- Read the [documentation](docs/)
- Submit an [Issue](https://github.com/hexagon-codes/hexagon/issues)

## License

By contributing code, you agree that your contributions will be released under the [Apache License 2.0](LICENSE).
