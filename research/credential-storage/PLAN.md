# Credential-recipe plan

Status: approved work plan, 2026-09-20.

## Target outcome

Determine whether and how the selected macOS preference values are transformed,
what class of data they represent, and whether local recovery is useful and
appropriate for this bridge. If the evidence supports a device-derived encrypted
envelope and the safety/usefulness gate passes, produce a versioned specification
and Go implementation that can decrypt and, for tests, encrypt it when given
explicit authorized inputs.

Completion means more than obtaining plausible output. Every input, transform,
constant, and layout decision must be supported by static control flow or an
account-independent synthetic experiment. Final real-value validation must use a
disposable authorized profile, and no general recovery implementation is published
unless the pre-publication gate concludes that it has a necessary interoperability
purpose and proportionate safeguards.

## Evidence levels

Every conclusion uses one of these labels:

- **Observed/static:** established directly from control flow or call arguments in
  the versioned binary.
- **Observed/synthetic:** reproduced with invented values without reading an
  account or contacting a server.
- **Observed/disposable:** validated against a disposable authorized profile.
- **Public prior art:** reported elsewhere but not independently reproduced.
- **Hypothesis:** a testable interpretation that must not enter implementation as
  fact.

## Work packages

### CS-0 — Baseline and artifact controls

1. Pin the client version, architecture, executable hash, operating system, and
   analysis date to the existing client inventory.
2. Assign a non-sensitive artifact ID to the binary and each private output set.
3. Create the private directory layout and manifest under
   `.lab/credential-storage/`; record hashes there, never proprietary artifacts.
4. Record the pinned external note's claims separately from our observations.
5. Confirm the official application remains untouched and stop it before analysis
   that uses the instrumented copy.

Exit gate: another session can locate the authorized inputs and reproduce queries
without any public account identifier or secret.

### CS-1 — Recover the preference read/write graph

1. Locate both class-level and instance-level secure preference writers and
   determine whether one is a wrapper.
2. Locate the matching read/decrypt path by following the fixed preference-key
   references, defaults access, and callers—not by guessing method names.
3. Trace values entering the data, preference-key, and secure-key arguments.
4. Identify how candidate string/key references map to stored values and determine
   whether the candidates use the same recipe.
5. Record cleanup, missing-value, decode-error, and integrity-failure behavior.
6. Produce a private call graph and a public behavior-only flow description.

Exit gate: the full path from stored bytes to returned decoded/transformed output,
and the inverse write path, are identified with no unexplained transform-producing
call between them.

Decision branch: if CS-1 disproves the hypothesized encrypted PBKDF2/CommonCrypto
path, record the falsification in the evidence ledger and revise or stop CS-2
through CS-4. Do not force a KDF/cipher interpretation onto a different transform.

### CS-2 — Recover device input and PBKDF2 parameters

Run this package only if CS-1 confirms that a KDF participates in the candidate
value path.

Trace and record the exact bytes used for:

1. platform identifier retrieval and error fallback;
2. text encoding, normalization, case changes, separators, prefixes, and suffixes;
3. PBKDF2 password/input and salt;
4. PRF selection;
5. iteration count;
6. derived-key length;
7. any split or secondary derivation of encryption and authentication keys.

The specification must distinguish strings from encoded bytes at every boundary.
If parameters differ by preference key or client version, define separate profiles
instead of conditional folklore.

Exit gate: a standalone function using invented platform identifiers reproduces
the client's derived bytes in a synthetic comparison.

### CS-3 — Recover the cipher and stored envelope

Run this package only if CS-1 confirms encryption or an equivalent keyed transform.

At the CommonCrypto construction and finalization sites, establish:

1. algorithm and key size;
2. mode of operation;
3. padding and options;
4. IV or nonce length, source, and per-write behavior;
5. associated data, if any;
6. authentication tag or separate MAC construction, if any;
7. exact byte ordering of version fields, IV/nonce, ciphertext, tag/MAC, and other
   metadata;
8. base64 behavior and whether length checks occur before or after decoding.

Do not assume the prior-art guess of `IV || ciphertext || tag`. A 96-byte value is
also compatible with unauthenticated block-cipher layouts. The read path must prove
the inverse layout.

Exit gate: encryption and decryption are described as byte-level algorithms with
all lengths and failure cases specified.

### CS-4 — Synthetic confirmation

Preferred order:

1. derive vectors from the static specification using invented identifiers and
   plaintext;
2. compare PBKDF2 and cipher intermediates with pure local methods in the
   instrumented copy;
3. hook crypto entry points only when a parameter remains ambiguous;
4. if the complete preference writer must run, intercept its terminal persistence
   call and route the invented output to an in-memory sink without calling the real
   writer; if that is not technically sound, use a dedicated disposable macOS user
   whose Kakao container has never held an account.

Never write a synthetic value into the official KakaoTalk preference domain. Never
print runtime buffers unless every byte was invented for the experiment. The
re-signed copy is valid evidence for pure computation, not for Keychain identity,
sandbox ownership, or server acceptance. Changing only the bundle identifier is
not sufficient isolation because it can alter the very container behavior being
tested. Before and after any writer experiment, hash the official preference file
and record privately that both its hash and metadata are unchanged.

Exit gate: independent client and Go implementations agree on deterministic test
vectors, and mutations of ciphertext/tag/padding fail as specified.

### CS-5 — Pre-publication safety and usefulness gate

Before writing a complete public recipe or general decryptor, determine:

1. the coarse data category represented by each candidate value;
2. whether local recovery is necessary for an authorized secondary-device bridge;
3. whether the result is portable or bound to the original Mac/profile;
4. whether a narrower Mac-side helper or official registration flow meets the need;
5. the likely abuse value of publishing the complete recipe;
6. safeguards that materially constrain the implementation to explicit,
   operator-owned inputs.

If the decoded object is not useful for interoperability, or a general recovery
tool's primary utility would be credential theft, stop at private findings and a
public high-level conclusion. Escalate ambiguous publication decisions to the
maintainer before implementation.

Exit gate: a written decision authorizes full specification/implementation and
identifies its minimum necessary interface and safeguards.

### CS-6 — Public specification and transfer review

1. Write a behavior-only recipe using synthetic names and vectors.
2. Assign confidence and provenance to each parameter.
3. Review for proprietary expression, offsets, internal names, secrets, device
   identifiers, and unnecessary credential-theft utility.
4. Record the transfer review in `EVIDENCE.md`.
5. Give the approved specification—not private analysis output—to the implementation
   agent.

Exit gate: an agent without binary access can implement the algorithm solely from
the public specification and synthetic tests.

### CS-7 — Guarded Go implementation

Design constraints:

- keep it separate from LOCO transport and Matrix/Beeper code;
- accept explicit ciphertext and device inputs initially; do not scan a home
  directory implicitly;
- select a recipe through an exact supported-version profile;
- return a typed unsupported-version error rather than guessing;
- reject malformed lengths and authentication/padding failures;
- avoid logging plaintext, input identifiers, keys, or intermediate buffers;
- minimize secret lifetimes and clear mutable buffers where practical;
- include only synthetic fixtures in Git.

Tests must cover the published vector, malformed base64, truncated envelopes,
unsupported versions, incorrect identifiers, corrupted ciphertext/integrity data,
and round-trip behavior where encryption is implemented.

Exit gate: formatting, unit tests, vetting, linting, vulnerability checks, and
secret scanning pass; a clean-room transfer reviewer confirms provenance.

### CS-8 — Disposable-profile validation

This phase waits for an official Mac client to have a populated disposable lab
profile. It does not require sending the recovered value to a server.

1. Copy only the minimum encrypted values into a permission-restricted private
   artifact location.
2. Record hashes and client version privately; never record plaintext in files or
   command output.
3. Run a no-plaintext-output verifier that reports only recipe match/failure and a
   coarse, pre-approved data category. Do not report field names or lengths unless
   a separate transfer review establishes that they are non-identifying and needed.
4. Cross-check that the official client can still read its value; do not mutate the
   official preference file.
5. Destroy unnecessary plaintext and temporary copies after the result is recorded.
6. Promote only the boolean result and pre-approved coarse category into the public
   ledger.

Exit gate: the versioned recipe successfully processes an authorized real value,
or the discrepancy is reduced to a documented falsified assumption and the plan
returns to the relevant work package.

### CS-9 — Final product decision

After validation, decide whether local credential reuse belongs in the bridge:

- Does the value represent a reusable session credential, a wrapping key, or
  another object?
- Is it bound to the Mac device identity, network, account, or application version?
- Can it be moved safely to a Linux homelab, or must a Mac-side helper mediate it?
- What revocation and rotation behavior applies?
- Did real-value validation change the CS-5 threat model or publication decision?
- Is local recovery safer and more maintainable than completing the official
  secondary-device registration flow?

The default on recipe mismatch is an actionable unsupported-version error and a
safe login fallback. Never silently retry server authentication.

## Estimated effort

- Static graph, KDF, and cipher recovery: 0.5–2 focused days.
- Synthetic confirmation and specification: 0.5–1 day.
- Guarded Go implementation and tests: 0.5–1 day.
- Expected total before real-value validation: 2–5 days.

The final validation schedule depends on a populated disposable Mac profile. That
dependency does not block CS-0 through CS-7 when the CS-5 gate authorizes public
specification and implementation.

## Stop conditions

Pause and reassess if:

- a path requires primary-account data;
- a result would require disabling host-wide security controls;
- the recipe depends on inaccessible hardware-backed material;
- only the re-signed build exhibits the behavior;
- the evidence cannot distinguish two materially different algorithms;
- completing the work would require publishing live or identifying artifacts.
