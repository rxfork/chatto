# SPDX-FileCopyrightText: 2026 ChattoCorp GmbH
# SPDX-License-Identifier: AGPL-3.0-or-later

# The official Debian image includes mise, curl, git, and CA certificates.
FROM ghcr.io/jdx/mise:2026.10.4-debian

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash build-essential ffmpeg gh lsof openssh-client pkg-config procps sudo unzip xz-utils \
    && rm -rf /var/lib/apt/lists/*

RUN useradd --create-home --uid 1000 --shell /bin/bash vscode \
    && echo 'vscode ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/vscode \
    && chmod 0440 /etc/sudoers.d/vscode

USER vscode
RUN echo 'eval "$(mise activate bash)"' >> /home/vscode/.bashrc

# The mise image has a CLI entrypoint; a dev container needs a running shell host.
ENTRYPOINT []
CMD ["sleep", "infinity"]
