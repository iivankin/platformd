# Private runtime payload

These files are release inputs, not host configuration. The release pipeline
adds exact `crun`, `conmon`, `netavark`, and `catatonit` binaries beside them,
then appends the verified runtime directory to the platformd executable.

Every absolute helper/config path resolves through the atomic `current`
release symlink. Platformd never searches host `/etc/containers` or `PATH`.
