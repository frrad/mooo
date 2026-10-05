# Bridge login ownership

Import and QR enrollment use the bridge framework's `User.NewLogin` with
`DontReuseExisting: true`. This is an intentional fail-closed policy: a login
ID may already have a live connector and an exclusive Kakao profile lease, and
the framework's default reuse path calls `LoadUserLogin` on the existing object
after mutating its metadata. Reusing that ID would silently replace the active
client owner.

Duplicate enrollment therefore fails while leaving the cached login, metadata,
client, and profile lease untouched. QR success material is installed before
the framework save step; if SQLite insertion or duplicate-login rejection then
fails, the recovery profile remains available for an explicit retry or import.
