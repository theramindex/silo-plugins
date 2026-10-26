# Shared Live TV media code

The Ramindex Live TV plugins use these packages for guarded source fetching,
codec-preserving relay, GPU compatibility sessions, and viewer-owned rewind.
The code has no Silo RPC dependency; plugins keep their own source resolution,
account reservations, UI routes, and recorder integration.

- `httpclient`: dial-time destination checks, provider error redaction, and
  bounded connection pools that never mix different destination allowlists.
- `transcode`: authenticated-viewer session capabilities, streaming relay,
  startup fallback, and GPU-only H.264/AAC HLS conversion. Two advisory GPU
  slots are shared across plugin processes in the same container. Process
  termination releases the slots; Linux also kills orphaned encoder children.
- `rewind`: keyframe-aligned H.264 MPEG-TS buffering and viewer-bound leases.
  Optional release callbacks return tuner/account reservations on stop/expiry.

Relay clients must already enforce the source's destination policy. A plugin
must expose media through authenticated routes, validate each viewer with
`RelayURL` or `Media`, and retain relative HLS asset references under its own
installation URL. Source and account selection stay outside this library.
