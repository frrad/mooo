# SL-EXP-002 — registration HTTP transport

- Date: 2026-09-20
- Researcher: mooo project
- Client/platform version: KakaoTalk for macOS 26.8.0, arm64
- Public artifact ID: SL-ART-MAC-ARM64-001
- Private artifact reference: targeted registration HTTP static-analysis report
- Evidence class: static

## Question

What common HTTP contract sits below the seven known registration request builders?

## Authorization and safety boundary

Offline static analysis used an authorized local executable. No application,
account, credential, live identifier, cookie store, or network request was used.

## Hypothesis

The operations share one ordinary HTTP/form transport without an additional
registration-specific signing layer.

## Method

Trace each builder into the shared request type, host provider, HTTP method and
encoder selection, explicit headers, request submission, response serializer, and
operation-specific completion adapter.

## Sanitized observation

All seven operations use `https://katalk.kakao.com`, POST, URL-form bodies with
nested device dictionaries, JSON responses, and shared HTTP-status validation.
Their explicit header collections are empty and they add no signature, nonce,
digest, authorization transform, or per-request interceptor. Platform defaults may
still provide ordinary headers and cookie behavior.

Shared error dictionaries use `reason`, `detailCode`, and `status`; absence of an
HTTP response becomes numeric `500`, while cancellation remains distinct.
Password check succeeds only for HTTP 200 plus an integer response `status` of zero.

## Conclusion

The hypothesis is supported with high confidence. QR check-key validation and
passcode numeric status assignments remain blocked.

## Cleanup

Raw binary output remains in private lab storage. No runtime state changed.

## Follow-up

Create invented form-body vectors and isolate the QR check-key validator if a safe
pure-helper path is found.
