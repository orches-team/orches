![ORCHES logo](https://raw.githubusercontent.com/orches-team/common/main/orches-logo-text.svg)
# orches: Simple git-ops for Podman
 [![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

podman run --rm -it  --mount type=bind,source=/run/user/$(id -u)/systemd,destination=/run/user/$(id -u)/systemd,rw=true -v ~/.config/orches:/var/lib/orches -v ~/.config/containers/systemd:/etc/containers/systemd --userns=keep-id --pid=host --env XDG_RUNTIME_DIR=/run/user/$(id -u) ghcr.io/orches-team/orches init https://github.com/orches-team/orches-config-rootless.git
