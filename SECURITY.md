# Security

tf-exclude does not talk to cloud APIs and does not read Terraform state except by running the `terraform` binary you already have (`plan`, `show`, `graph`, and the command you asked for). It does not run `terraform state` subcommands.

If you believe you found a vulnerability, open a private security advisory on this repository if GitHub lets you, or contact the maintainer, Rafael de Carvalho, another way you already use for this project. Please include the tf-exclude version, the Terraform version, and a configuration that reproduces the issue without live cloud credentials.

There is no bug bounty.

Do not open a public issue for an unfixed vulnerability.
