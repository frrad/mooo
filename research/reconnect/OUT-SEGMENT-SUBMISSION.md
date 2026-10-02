# Out-segment submission adapter

Status: clean-room implementation seam; opt-in and transport-independent from
the default `Session` constructor.

The adapter accepts an injected asynchronous write worker. The worker's
`Submit` method returns after submission and delivers progress and terminal
`OutSegmentWriteResult` callbacks. A positive incomplete socket write reports
its delta immediately and disarms the delayed owner while the request remains
pending; the terminal callback follows when the worker finishes. The adapter
queues out-segment enable only after submission returns. Each callback queues
the reviewed disable first, then forwards its result tuple to the caller. A
callback racing the submission return is held until enable admission completes,
so it cannot reverse that order.

The adapter's Go result observer is an explicit integration hook for the
caller; it does not rename or replace the source contract's empty `didWrite`
callback. The opt-in Session binding correlates non-ambiguous non-context write
errors to the exact pending request and disarms any committed receive timeout;
it does not affect another request's generation.

An explicitly ambiguous partial-write result is terminal for this Go adapter:
the worker is closed, future submissions are rejected, and no retry is
attempted. Context cancellation before bytes are written, or after a complete
frame, remains reusable. `Close` invalidates pending callback generations,
closes the owner, and asks the worker to interrupt in-flight work. The
Session-level `Shutdown(ctx)` is the external teardown path that joins the
opt-in worker; callback-side `Close` remains interrupt-only so a callback
cannot self-join. A callback already selected by the adapter may finish
outside its mutex.

The adapter does not choose producer status, timeout configuration, or initial
admission. Its opt-in Session binding reuses the existing serialized write
worker, socket encryption/serialization path, and context cancellation; the
default Session constructor remains unchanged. A non-positive timeout skips
the optional timer arm while allowing the submitted write and callback to
complete. The net.Pipe tests establish bounded submission, callback, partial
progress, cancellation, and close behavior for this injected seam. Producer-
specific partial progress is intentionally limited to positive incomplete
writes; source callback status and configuration admission remain outside this
opt-in adapter.
