# gs-acm-utils

Offline tooling for OCM/ACM policy templates.

`acm-mock-resolver` resolves and validates `{{ ... }}` and `{{hub ... hub}}` policy
templates against a directory of mock Kubernetes objects, with no kubeconfig or
cluster required. It consumes
[stolostron/go-template-utils](https://github.com/stolostron/go-template-utils)
as a library.

Status: under construction. See `docs/` as it grows.

```
make build
bin/acm-mock-resolver version
```

Licensed under Apache-2.0; see `LICENSE` and `NOTICE`.
