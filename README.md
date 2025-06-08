![orches logo](https://raw.githubusercontent.com/orches-team/common/main/orches-logo-text.png)

# orches: Simple git-ops for Podman and systemd

[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

Content:

- [Overview](#overview)
- [Project Status](#project-status)
- [Quick Start](#quick-start)
- [CLI documentation](#cli-documentation)
- [Supported units](#supported-units)
- [FAQ](#faq)

## Overview

orches is a simple git-ops tool for orchestrating [Podman](https://podman.io/) containers and systemd units on a single machine. It is loosely inspired by [Argo CD](https://argo-cd.readthedocs.io/en/stable/) and [Flux CD](https://fluxcd.io/), but without the need for Kubernetes.

Containers in orches are defined by [Podman Quadlets](https://www.redhat.com/en/blog/quadlet-podman). A super simple example of such a file can look like this:

```ini
[Container]
Image=docker.io/library/caddy:2.9.1-alpine
PublishPort=8080:80

[Install]
WantedBy=multi-user.target
```

All you need to start using orches is to create a repository, include this file, and run `orches init REPO_PATH` to sync your local system with the repository content.

orches is not limited to Podman containers, but it is also able to manage generic systemd units. This makes it a great pick for managing both containerized, and non-containerized workloads. orches is able to run both system and [user](https://wiki.archlinux.org/title/Systemd/User) systemd units.

## Project Status

⚠️ **Project Maturity Warning**: orches is a young project that is currently being used on small production servers. While it is stable enough for basic production use, you may encounter rough edges.

## Quick Start

> **Tip:** For a ready-to-use rootless example with many popular services (like Jellyfin, Pi-hole, Homarr, and more), check out [github.com/orches-team/example](https://github.com/orches-team/example). This repository provides a comprehensive orches configuration you can fork or use as inspiration for your own setup.

orches can run both rootless and rootful. While running rootless offers stronger security, some applications cannot be run in such a setup. We provide sample configuration for both modes. If you are not sure which one to pick, start with rootless, it's simple to switch to rootful later if you need to.

In order to run orches, you need:

- podman >= 4.4
- systemd

orches has been tested on Fedora 41, Ubuntu 24.04, and CentOS Stream 9 and its derivates

### Initializing orches with a rootless config

To start using rootless orches, simply run the following commands:

```bash
loginctl enable-linger $(whoami)

mkdir -p ~/.config/orches ~/.config/containers/systemd

podman run --rm -it --userns=keep-id --pid=host --pull=newer \
  --mount \
    type=bind,source=/run/user/$(id -u)/systemd,destination=/run/user/$(id -u)/systemd \
  -v ~/.config/orches:/var/lib/orches \
  -v ~/.config/containers/systemd:/etc/containers/systemd  \
  --env XDG_RUNTIME_DIR=/run/user/$(id -u) \
  ghcr.io/orches-team/orches init \
  https://github.com/orches-team/orches-config-rootless.git
```

These commands:
1. Enable [lingering](https://wiki.archlinux.org/title/Systemd/User#Automatic_start-up_of_systemd_user_instances) for orches and managed apps to start on boot.
2. Create directories required by orches.
3. Initialize orches via its `init` subcommand, using the official rootless sample repository (containing orches and a caddy webserver). The specified `podman run` flags grant orches permission to control systemd user units.

Once you run the command, you should be able to verify that orches, and the webserver is running:

```bash
systemctl --user status orches
systemctl --user status caddy
podman exec systemd-orches orches status
curl localhost:8080
```

### Initializing orches with a rootful config

To start using rootful orches, simply run the following commands:

```bash
sudo mkdir -p /var/lib/orches /etc/containers/systemd

sudo podman run --rm -it --pid=host --pull=newer \
  --mount \
    type=bind,source=/run/systemd,destination=/run/systemd \
  -v /var/lib/orches:/var/lib/orches \
  -v /etc/containers/systemd:/etc/containers/systemd  \
  ghcr.io/orches-team/orches init \
  https://github.com/orches-team/orches-config-rootful.git
```

These commands:
1. Create directories required by orches.
2. Initialize orches via its `init` subcommand, using the official rootful sample repository (containing orches and a caddy webserver). The specified `podman run` flags grant orches permission to control systemd units.

Once you run the command, you should be able to verify that orches, and the webserver is running:

```bash
systemctl status orches
systemctl status caddy
sudo podman exec systemd-orches orches status
curl localhost:8080
```

### Customizing your deployment

You now have orches and up and running. Let's add an actually useful application, a [Jellyfin media server](https://jellyfin.org/), to the deployment. Firstly, you need to fork the template repository ([rootless](https://github.com/orches-team/orches-config-rootless), [rootful](https://github.com/orches-team/orches-config-rootful)) that you started with in the previous step.

Once you have your fork created, clone it locally, and add the following file as `jellyfin.service`:

```ini
[Container]
Image=docker.io/jellyfin/jellyfin
Volume=config:/config:Z
Volume=cache:/cache:Z
Volume=media:/media:Z
PublishPort=8096:8096

[Install]
WantedBy=multi-user.target default.target
```

Commit the file, and push to your fork. Now, it's time to tell orches to use your fork instead of the sample repository. Run the following command on your host running orches:

```bash
podman exec systemd-orches orches switch ${YOUR_FORK_URL}
```

You should now be able to navigate to <http://localhost:8096> and see your new Jellyfin instance.

### Updating your deployment

Now that you know how to deploy new containers, it's also time to learn how to modify, or remove existing ones.

Firstly, let's modify the Jellyfin one to automatically restart itself if it fails:

```diff
  [Container]
  Image=docker.io/jellyfin/jellyfin
  Volume=config:/config:Z
  Volume=cache:/cache:Z
  Volume=media:/media:Z
  PublishPort=8096:8096

+ [Service]
+ Restart=on-failure

  [Install]
  WantedBy=multi-user.target default.target
```

Secondly, delete the sample webserver (`caddy.container`) from the repository. Now, commit all changes, and push them to your remote repository.

orches checks for changes and applies them every 2 minutes. If you are impatient, you can trigger a sync manually with `podman exec systemd-orches orches sync`. After either 2 minutes, or a manual sync, you should see the jellyfin service restarted, and the caddy service removed. You can check this with:

```bash
systemctl status jellyfin
systemctl status caddy
```

### Removing orches

If you want to remove orches altogether, just run:

```bash
podman exec systemd-orches orches prune
```

## CLI documentation

This section describes orches CLI and all its flags and subcommands.

### Global flags

| Flag        | Description                                                                           |
|-------------|---------------------------------------------------------------------------------------|
| `--dry`     | Instructs orches to just print what it would do, but no changes are actually applied. |
| `--verbose` | Turns on verbose logging.                                                             |


### `orches init REF`

Initializes orches from the given `REF`. `REF` accepts the same formats as `git clone` does.

### `orches sync`

Instructs orches to check for changes in the target repository, and apply them.

### `orches run`

Starts orches as a daemon. This basically runs `orches sync` every 2 minutes. Send SIGINT (ctrl+C), or SIGTERM to stop.

Flags:

| Flag         | Default | Description                                |
|--------------|---------|--------------------------------------------|
| `--interval` | 120     | How often the sync is performed in seconds |

### `orches switch REF`

Switches orches to deploy from `REF` instead of its current target. `REF` accepts the same formats as `git clone` does.

### `orches prune`

Stops and removes all units managed by orches, and removes the local checkout of the repository - returning the system to a pristine state.

### `orches status`

Prints information about the current target and the deployed commit. The output format is yaml.

### `orches version`

Prints orches version and some details about its build. The output format is yaml.

## Supported units

Orches supports the following unit types:

| File extension | Description                                                                                                             |
|----------------|-------------------------------------------------------------------------------------------------------------------------|
| `.container`   | Podman [container unit](https://docs.podman.io/en/latest/markdown/podman-systemd.unit.5.html#container-units-container) |
| `.network`     | Podman [network unit](https://docs.podman.io/en/latest/markdown/podman-systemd.unit.5.html#network-units-network)       |
| `.service`     | Ordinary [systemd service](https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html)                |


orches only process units in the top level directory of the repository. All directories in the repository are currently ignored.

Additionally, all units with unknown extensions are ignored. You can use this to your advantage. Simply rename `web.container` to `web.container.ignored`, and orches will remove this container during the next sync.

Podman units [cannot be enabled](https://docs.podman.io/en/latest/markdown/podman-systemd.unit.5.html#enabling-unit-files), orches only runs start/stop/try-restart one them. Plain systemd service units are also enabled, or disabled.

Units are restarted when a change in them is detected. The algorithm is naive, it just compares the old file, and the new one byte by byte.

## FAQ

This is a list of practical Frequently Asked Questions about running orches.

### Can I use a private repository?

Certainly! It's recommended to start with a public fork of one of the starter repositories. Make sure that your are using the SSH remote path when running `switch`. Once you have your deployment switched, copy an unencrypted private ssh key to the server and add a volume to your `orches.container`:

```ini
Volume=PATH_TO_YOUR_SSH_KEY:%h/.ssh/id_rsa
```

Now sync your deployment, make your fork private, and sync it again to verify that orches can still pull from the repository.


### Can I also manage configuration files for my containers using orches?

Yes, orches makes it easy to manage configuration files alongside your container unit files. Here's how it works:

1. Store your configuration files in your orches-managed git repository alongside your unit files
2. Reference these files in your container units using relative paths
3. Use the `X-Version` key in your unit files to trigger container restarts when configs change

> The `X-Version` field is needed because orches only detects changes to the unit files themselves, not to external files referenced by them. When you update a configuration file, orches won't automatically know to restart the container since the unit file hasn't changed. By incrementing `X-Version`, you force the unit file to be different, which triggers orches to restart the container with the new configuration.

**Rootless Example (`~/.config/orches/repo`):**
```ini
[Container]
Image=docker.io/library/caddy:alpine
Volume=%h/.config/orches/repo/Caddyfile:/etc/caddy/Caddyfile:z
X-Version=1

[Install]
WantedBy=multi-user.target default.target
```

**Rootful Example (`/var/lib/orches/repo`):**
```ini
[Container]
Image=docker.io/library/caddy:alpine
Volume=/var/lib/orches/repo/Caddyfile:/etc/caddy/Caddyfile:z
X-Version=1

[Install]
WantedBy=multi-user.target default.target
```

When you update a configuration file in your repository, increment the `X-Version` value in the corresponding container unit file. This ensures orches restarts the container with the new configuration during the next sync.

### Can I just use `:latest` instead of pinning my container images?

You can use `:latest` or any other floating tag, but it's generally discouraged for production deployments because it can lead to unexpected updates and breakages. If you do want to use auto-updating images, Podman supports this via the `AutoUpdate=registry` option in your Quadlet file. 

When you enable auto updates for your containers with the line `AutoUpdate=registry` in a Quadlet file you also need to enable and start the `podman-auto-update` service for that specific user (i.e. `systemctl --user enable podman-auto-update`), otherwise it won't update the image(s).

For more information, see the [Podman auto-update documentation](https://docs.podman.io/en/latest/markdown/podman-auto-update.1.html).
