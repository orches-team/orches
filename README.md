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


```bash
# TODO: lingering
mkdir -p ~/.config/orches ~/.config/containers/systemd

podman run --rm -it --userns=keep-id --pid=host \
  --mount type=bind,source=/run/user/$(id -u)/systemd,destination=/run/user/$(id -u)/systemd,rw=true \
  -v ~/.config/orches:/var/lib/orches \
  -v ~/.config/containers/systemd:/etc/containers/systemd  \
  --env XDG_RUNTIME_DIR=/run/user/$(id -u) \
  ghcr.io/orches-team/orches init \
  https://github.com/orches-team/orches-config-rootless.git
```
