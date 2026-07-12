# Pinned libpod source

This directory contains only the Podman packages reachable from platformd's
embedded libpod adapter. The source is mechanically copied from Podman v5.8.2,
commit `5b263b5f5b48004a87caac44e67349a8266d9ef4`, under the upstream Apache-2.0
license in `LICENSE`.

Platformd carries four narrow integration extensions because libpod is
normally linked into the Podman CLI:

- `libpod.WithSignalHandling(false)` prevents libpod from owning process-wide
  SIGINT/SIGTERM handling and calling `os.Exit` behind the daemon;
- `libpod.WithConmonCleanupCommand(false)` prevents conmon from invoking the
  platformd executable with Podman CLI cleanup arguments. Platformd waits for
  exits and calls libpod cleanup itself; startup purges every stale container
  record and writable layer after a crash;
- `libpod.WithLogRotation(maxFiles)` passes conmon's native size-rotation
  policy without platformd renaming an active log file;
- `Container.ExecContext` provides attached exec cancellation without routing
  the operation through the Podman CLI or its API service.

No compatibility shim is provided. Updating the pinned source is a hard
cutover: regenerate the reachable package set, reapply/review these options,
and pass the privileged runtime contract suite on every supported host.
