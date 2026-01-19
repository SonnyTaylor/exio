# Contributing to Exio

Thank you for your interest in contributing to Exio! This document provides guidelines and instructions for contributing.

## Development Setup

### Prerequisites

- Go 1.22 or later
- Git
- Make (optional, for using Makefile commands)

### Getting Started

1. Fork the repository on GitHub
2. Clone your fork locally:
   ```bash
   git clone https://github.com/YOUR_USERNAME/exio.git
   cd exio
   ```

3. Add the upstream remote:
   ```bash
   git remote add upstream https://github.com/SonnyTaylor/exio.git
   ```

4. Install dependencies:
   ```bash
   go mod download
   ```

5. Build the project:
   ```bash
   make build
   ```

6. Run tests:
   ```bash
   make test
   ```

## Project Structure

```
exio/
├── cmd/
│   ├── exio/           # Client CLI entry point
│   └── exiod/          # Server daemon entry point
├── pkg/
│   ├── protocol/       # Shared types and constants
│   ├── transport/      # WebSocket + Yamux transport layer
│   └── auth/           # Authentication utilities
├── internal/
│   ├── server/         # Server implementation
│   └── client/         # Client implementation
├── deploy/             # Deployment configurations
└── docs/               # Additional documentation
```

## Making Changes

### Branching Strategy

- Create feature branches from `main`
- Use descriptive branch names: `feature/add-tcp-tunneling`, `fix/reconnection-bug`

### Code Style

- Follow standard Go conventions and idioms
- Run `go fmt` before committing
- Run `go vet` to check for common mistakes
- Use meaningful variable and function names
- Add comments for exported functions and types

### Testing

- Write tests for new functionality
- Ensure all tests pass before submitting: `make test`
- Aim for good test coverage on critical paths
- Use table-driven tests where appropriate

### Commit Messages

Write clear, concise commit messages:

```
Short summary (50 chars or less)

More detailed explanation if necessary. Wrap at 72 characters.
Explain the problem this commit solves and why the change was made.

- Bullet points are okay
- Use present tense ("Add feature" not "Added feature")
```

## Pull Request Process

1. Update documentation if you're changing functionality
2. Add tests for new features
3. Ensure CI passes (tests, linting)
4. Request review from maintainers
5. Address review feedback
6. Squash commits if requested

### PR Checklist

- [ ] Tests added/updated
- [ ] Documentation updated
- [ ] `go fmt` and `go vet` pass
- [ ] All tests pass
- [ ] Commit messages are clear

## Reporting Issues

### Bug Reports

Include:
- Exio version (`exio version`)
- Operating system and version
- Steps to reproduce
- Expected vs actual behavior
- Relevant logs or error messages

### Feature Requests

Include:
- Clear description of the feature
- Use case / motivation
- Potential implementation approach (if you have ideas)

## Code of Conduct

- Be respectful and inclusive
- Provide constructive feedback
- Focus on the code, not the person
- Help others learn and grow

## Getting Help

- Open an issue for questions
- Check existing issues before creating new ones
- Tag issues appropriately

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
