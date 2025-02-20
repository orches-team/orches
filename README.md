![orches logo](https://raw.githubusercontent.com/orches-team/common/main/orches-logo-text.svg)
# orches: Simple git-ops for Podman and systemd
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

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

## Quick Start
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

These commands perform the following steps:

1) Enabling [lingering](https://wiki.archlinux.org/title/Systemd/User#Automatic_start-up_of_systemd_user_instances) in order to launch orches and the apps it manages when the system boots.
2) Creating directories that orches needs to run.
3) Initializing orches by running its `init` subcommand. The extra flags given to `podman run` are needed so orches can control systemd user units. The last argument controls which repository is used for the initial deployment. In this case, the official rootless sample with orches and a dummy [caddy](https://caddyserver.com/) webserver is used.

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

These commands perform the following steps:

1) Creating directories that orches needs to run.
2) Initializing orches by running its `init` subcommand. The extra flags given to `podman run` are needed so orches can control systemd user units. The last argument controls which repository is used for the initial deployment.  In this case, the official rootless sample with orches and a dummy [caddy](https://caddyserver.com/) webserver is used.

Once you run the command, you should be able to verify that orches, and the webserver is running:

```bash
systemctl status orches
systemctl status caddy
status podman exec systemd-orches orches status
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

You should now be able to navigate to http://localhost:8096 and see your new Jellyfin instance.

### Wrapping it up
Now that you know how to deploy new containers, it's also time to learn how to modify, or remove existing ones.

Firstly, let's

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

