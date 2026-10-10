# SPDX-FileCopyrightText: 2026 ChattoCorp GmbH
# SPDX-License-Identifier: AGPL-3.0-or-later

FROM mcr.microsoft.com/devcontainers/base:ubuntu-24.04

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        build-essential ffmpeg gh lsof pkg-config \
    && rm -rf /var/lib/apt/lists/*

USER vscode
RUN echo 'eval "$(mise activate bash)"' >> /home/vscode/.bashrc
