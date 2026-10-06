# Parity fixtures and tests

This is the rule for turning official-client research into tests. It exists
because a test that defines its own model of the official client and checks
that model against a fixture compares the model with itself: it always passes
and says nothing about mooo.

## Two halves, one fixture

A parity fixture is a JSON file of synthetic inputs and the official client's
expected outputs. Two independent consumers read the same file:

1. **mooo, in CI.** A Go test under `internal/` feeds each case's inputs to
   production code (the real decoder, framing, receive path, reducer, or bridge
   connector) and compares the result with the expected output. This is the
   only kind of parity test that belongs in `internal/`.
2. **The official client, in the lab.** A lab harness (for example a
   `cmd/mooo-lab` subcommand driving a Frida script on the owned emulator)
   feeds the same inputs to the official client's own code and confirms or
   rewrites the expected outputs. It runs on demand, never in CI, and its
   output is sanitized before it is committed.

The fixture is the contract between them. Neither half embeds a model of the
other.

## Provenance

Every parity fixture carries a top-level `provenance` object:

```json
"provenance": {
  "kind": "static",
  "client": "KakaoTalk for Mac <version>",
  "date": "2026-10-03",
  "method": "Ghidra read of the dictionary decoder and its callers",
  "doc": "research/reconnect/RC-Q5-INVALID-UTF8.md"
}
```

`kind` is one of:

- `static`: expected outputs were derived by reading decompiled code. The
  official client has not run these inputs.
- `executed`: a lab harness ran these inputs through the official client's
  code. `method` names the harness and its invocation.
- `observed`: the outputs come from a controlled live experiment on an owned
  account. Use this for stateful behaviour that cannot be called in isolation
  (reconnect, timers, receipts). `method` points at the experiment record.

`client` names the exact build. A fixture whose `kind` is `executed` or
`observed` is pinned to that build; re-run the harness or experiment when the
reference client changes.

## Rules

- **Tests exercise production code.** A `_test.go` file under `internal/` must
  call into mooo. `internal/testpolicy` enforces a mechanical floor: each test
  file must reference a production declaration of its own package or an
  exported symbol of another non-testsupport package in this module. Satisfying
  the lint by touching an unrelated symbol does not satisfy the rule.
- **Parity claims need `executed` or `observed` fixtures.** Code, docs, and PR
  descriptions may say mooo matches the official client only against those.
  A test against a `static` fixture is still worth running; a failure is a
  lead to investigate, not proof of a difference.
- **Differences are red first.** When a fixture shows mooo differing from the
  official client, land a failing test (or fix it in the same PR, with the test
  shown failing before the fix). Then either fix mooo or record a deliberate
  deviation.
- **Deliberate deviations are explicit.** When mooo intentionally differs, for
  example because Matrix requires valid UTF-8, the case says so with a
  `mooo_deviation` string giving the reason and a `mooo_expected` value the
  test asserts instead.
- **Findings without production code stay in `research/`.** If no mooo code
  implements the behaviour yet, commit the fixture and its document under
  `research/` and stop. Do not write a Go model of the official client to give
  it test coverage, and do not add production code that only tests call. A
  finding gets a Go test when the code that consumes it lands.
- **Fixtures live under `research/fixtures/<area>/`.** Go tests read them by
  relative path so the lab harness and CI share one copy.
- **Inputs are synthetic.** No account data, identifiers, captured traffic, or
  copied proprietary code, as everywhere else in this repository.

## Migrating existing fixtures

Fixtures written before this rule carry a free-form `status` string such as
`reviewed-static-unexecuted-runtime` and were consumed by model-only tests.
Migration moves each one to `research/fixtures/`, adds `provenance` (`static`
for all of them today), deletes its model-only test, and, where production
code exists, replaces it with a test against that code. The files still
awaiting migration are listed in
`internal/testpolicy/testdata/model-only-allowlist.txt`; the list only shrinks.
