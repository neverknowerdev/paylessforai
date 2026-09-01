# Self-updater binary E2E

This suite launches the real supervisor/application binary against an in-process
mock GitHub Releases API. Each scenario gets a fresh temporary database and
data directory, and validates persisted promotion, rollback, diagnostics, and
automatic retry quarantine.

Run it from the repository root with:

```sh
GOCACHE=/tmp/paylessforai-go-cache go test -tags e2e ./test/updater_e2e -count=1 -v
```

The test is intentionally separate from the browser E2E suite because it
builds and supervises real candidate binaries rather than testing only the UI.
