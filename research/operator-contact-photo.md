# Operator contact profile-photo retrieval

Research date: 2026-10-09. Sources: authorized macOS KakaoTalk 26.8.0 arm64
static traces and controlled owned Android 26.8.2 account observations. No public
prior implementation was consulted. Static source findings are leads; the live
acceptance below establishes the tested path only.

## Official chain

The existing [MEMBER dossier](chat-metadata.md) traces request/response models,
coordinator batching, error completion and user-record persistence. `pi` becomes
`profileImageUrl`; full/original variants are separate properties. An ordinary
profile-view consumer cancels its previous download, updates from its user record,
and uses a local default image when `profileImageUrl` is empty. Otherwise it
passes that URL to its image-loading path.

That path checks the shared image cache, updates immediately on a cache hit,
or displays a placeholder and starts `downloadImageFromURL:success:failure:`.
The download task is retained and can be cancelled. The inspected downloader
implementations make a GET through their HTTP managers and configure response
serialization. The success path clears the retained task and dispatches the
image update: NSData is decoded to NSImage, an NSImage is accepted directly,
and unsupported/nil results do not update the view.

Gaps: concrete downloader receiver binding at the indirect call, its failure
callback, cache insertion/expiry/invalidation, HTTP manager credentials and
redirect policy remain untraced. An initially guessed callback resolved to a
property accessor and supplies no failure evidence. Animated/original/full-size
profile consumers have separate branches; this work does not claim their parity.
Private source reports remain outside Git. No proprietary implementation is copied.

## Implementation and operator command

```sh
mooo-lab contacts photo --state /absolute/private/profile --chat CHAT_ID --user USER_ID --output /absolute/private/avatar.jpg
```

The selected profile must be operator-owned. Stop its bridge first; this command
uses the existing exclusive profile lease. Both paths must be absolute and the
output parent private (0700). Output is created exclusively at 0600, never
replacing an existing file. Image format comes from validated bytes, not the
filename. Keep output outside Git. Console output contains only a byte count or
a generic error; an absent photo is reported explicitly.

`Client.ContactProfilePhoto` requests one MEMBER profile in the selected room,
requires the response room ID and exactly one matching user ID, and downloads
its current `pi` thumbnail. Missing, extra or mismatched profiles fail closed.
Self-profile lookup is explicitly unsupported. This is a room-scoped contact
lookup, not friend discovery, an all-contact export or full-size/animated retrieval.
An empty URL returns `ErrContactPhotoAbsent`; no local official asset is invented.

Each explicit invocation fetches fresh MEMBER metadata; URLs are not stored or
cached. A failed request is not automatically retried. A later caller may retry
and receive a new resource URL. Unknown expiry is not guessed from URL spelling.
The command has a sixty-second operation deadline and the download has a
thirty-second deadline. Cleanup failure prevents successful output.

The bridge and operator share `media.DownloadAvatar`: HTTPS Kakao CDN only,
no URL credentials/explicit ports, bounded redirects, four-MiB body limit,
JPEG/PNG only, complete image decoding and at most sixteen million pixels with
an 8192 limit on either dimension. Resource requests do not attach Kakao account
credentials. Errors never contain source URLs. These are mooo safety decisions,
not claims about the official HTTP policy. Standard operator CLI downloads do
not configure cookies or authorization; the downloader strips the caller's cookie jar. Callers supplying their own
transport are responsible for any headers injected by that transport.

Normal failures remove the reserved output. Abrupt termination can leave an
incomplete file; inspect/remove it and choose a fresh output path. Operator
shadow diagnostics use logging, preserving explicit environment overrides.

## Controlled acceptance

An existing B secondary profile looked up its owned A peer. Initially A used the
default avatar: the command reported no profile photo, removed output and released
the lease. A then uploaded a synthetic four-quadrant image through its normal
Android profile editor. B's official Android profile view showed the new image.
The operator command retrieved the matching CDN thumbnail as validated image bytes,
created private output, released the lease, and preserved committed message
positions. No secondary login on A or read acknowledgement was needed.

Gallery edge case: Android's selected-photo permission did not include the newly
pushed fixture. The normal `Select More Photos` picker granted access only to that
synthetic image; the fixture was then selected and confirmed through preview and
profile-editor Done. Do not select an unverified first gallery tile: it can be
Camera or a previously selected image.

Production tests use synthetic wire replies and HTTP responses to cover exact
request identity, absent/mismatched/unsafe resources, expired-resource failure
followed by fresh metadata on an explicit retry, and private CLI cleanup. Existing
bridge avatar tests still exercise the shared downloader. A failing regression
first demonstrated that MIME sniffing accepted a truncated PNG; complete bounded
image validation fixes that failure for both consumers. A second failing
regression showed that a caller's cookie jar could send cookies to the CDN; the
shared downloader now strips that jar. These tests are not
executed official-client parity fixtures.
