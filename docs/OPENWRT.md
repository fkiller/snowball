# OpenWrt operations

Fresh installs follow [getting started](GETTING_STARTED.md). Existing production
installations retain the `snowball-voice` container, `/data`, Docker socket, and
state paths. Product display names do not change these identifiers.

## Optional existing-daemon boot integration

The installer/start helper/procd service were preserved from the router's
independent, uncommitted work. They target an **already-created** container in
the host's existing Docker daemon. They are optional and are not executed by
building or cloning this repository.

After creating and verifying a fresh container on that daemon:

```sh
SNOWBALL_DOCKER_HOST=unix:///var/run/docker.sock ./openwrt/install-snowball-voice.sh /root/snowball-voice
```

This installs the helper/service, enables the host Docker boot service, and
changes that existing container's restart policy. It does not create a new
container or volume. Do not use it on an existing dedicated-daemon deployment
until an approved migration has passed. Reboot acceptance is a manual gate.

## Optional daemon migration (maintenance downtime)

This is a migration candidate, not an instruction to change the current live
router. Confirm storage, a private backup, both daemon identities, and an
approved maintenance window. Existing user state is the source of truth.
Install `jq` using your router's supported package manager if necessary.

The helper refuses identical daemons, an existing target container/volume,
unsupported source mounts, and an unhealthy source. It transfers the exact
image, stops the old container **before** copying `/data`, creates the
replacement with preserved environment and the normal Snowball restrictions,
and tests health, administrator state, and idle authenticated browser state.
The original container and data remain for rollback. Temporary transfer data
contains credentials and is private; allow space for both image and data archives.

```sh
SNOWBALL_MIGRATE_APPROVAL=YES \
SNOWBALL_OLD_DOCKER_HOST=unix:///var/run/snowball-voice-docker.sock \
SNOWBALL_DOCKER_HOST=unix:///var/run/docker.sock \
./openwrt/migrate-to-existing-docker.sh
```

It never starts both containers on the same production ports. On failure it
removes only the replacement it created and restarts the original container.
If automatic rollback fails, the helper reports that explicitly; restore the
old container through its original daemon. Do not remove the original data.
After a successful migration, verify console authentication and a real media
cycle, install boot integration separately, and test reboot in another approved
window. Do not delete the old daemon/data as part of this procedure.

Mocked lifecycle/rollback tests do not establish physical migration acceptance.
The live router was not migrated during public-source preparation.

## Upgrade and rollback

Build the new source under a separate image tag with a clean checkout. Test
using disposable state and QA ports, never the live Chromium profile.
Privately back up `/data` consistently before the approved maintenance window.
Set `DOCKER_HOST` to the daemon that owns the live container.

```sh
export DOCKER_HOST=unix:///var/run/snowball-voice-docker.sock
docker build --network host -t snowball-voice:candidate .
SNOWBALL_QA_IMAGE=snowball-voice:candidate ./tools/run-qa-browser-smoke.sh
```

Only after approval and passing tests:

```sh
SNOWBALL_DEPLOY_APPROVAL=YES \
SNOWBALL_CANDIDATE_IMAGE=snowball-voice:candidate \
SNOWBALL_DEPLOY_IMAGE=snowball-voice:0.4.0-alpha.1 \
./tools/deploy-candidate.sh
```

The deployment helper retains the prior container as a rollback target and
preserves its existing `/data` mount. Review all reported health/auth/browser/
console checks. For manual rollback, stop/remove only the failed replacement,
rename the preserved rollback container to `snowball-voice`, and start it using
the same daemon. Check the exact names locally before any destructive command.
Never use `down -v`, erase NVS, or delete a user's credential volume.
