# Contributing

Issues and pull requests are welcome. Report a security problem privately through [GitHub's security advisories](https://github.com/vitzeno/detent/security/advisories/new), not a public issue.

## Building and testing

Needs Go 1.26.

```sh
make build      # bin/detent
make test       # go test -race ./..., as CI runs it
make lint       # golangci-lint, configured in .golangci.yml
make fmt        # gofmt
```

Two tests reach outside the process and skip unless asked: `DETENT_LIVE=1` runs the model tests against a real endpoint, and the containerd tests skip when no daemon answers.

## How the code is written

[CLAUDE.md](CLAUDE.md) is the guide: the architecture, the vocabulary and the Go style the code follows. Read it before a change of any size. In short:

- Everything is an event on the bus. A new feature is usually a subscriber, or a field on a fact.
- A test for a bug must fail without the fix. Put the bug back and watch it fail.
- Comments say why, in one line by default.
- A commit message is one subject line, with no body.

CI runs the race tests on Linux and macOS, the tests on Windows, lint, fuzzing and a cross-build. A pull request should pass all of it.
