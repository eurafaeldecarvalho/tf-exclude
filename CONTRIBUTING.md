# Contributing

Issues and pull requests are welcome. There is no guaranteed review.

- Bug reports and feature requests should use the templates in `.github/ISSUE_TEMPLATE`.
- One person may have at most **3 open pull requests** at a time. CI checks this on pull requests.
- Run `go test ./...` before opening a PR. Integration tests need Terraform and, for the parity cases, OpenTofu: `go test -tags=integration ./...`.
- Do not add a dependency unless the standard library cannot do the job.
- Do not change repository visibility, publish a release, or push a tag as part of an ordinary code change.

An AI coding agent wrote the initial implementation. Later changes should say so in the pull request when an agent wrote them.
