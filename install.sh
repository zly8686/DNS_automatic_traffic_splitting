#!/bin/bash

set -e

REPO="Hamster-Prime/DNS_automatic_traffic_splitting"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/doh-autoproxy"
BINARY_NAME="doh-autoproxy"
SERVICE_NAME="doh-autoproxy"

if [ "$EUID" -ne 0 ]; then
  echo "Please run as root"
  exit 1
fi

ARCH=$(uname -m)
case $ARCH in
  x86_64)
    ARCH="amd64"
    ;;
  aarch64)
    ARCH="arm64"
    ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

OS="linux"

echo "Detected system: $OS/$ARCH"

echo "Fetching latest release info..."
LATEST_TAG=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$LATEST_TAG" ]; then
  echo "Failed to fetch latest release tag. Please check your network or repo settings."
  exit 1
fi

echo "Latest version: $LATEST_TAG"

DOWNLOAD_URL="https://github.com/$REPO/releases/download/$LATEST_TAG/doh-autoproxy-$OS-$ARCH"
echo "Downloading $DOWNLOAD_URL..."

curl -L -o "$INSTALL_DIR/$BINARY_NAME" "$DOWNLOAD_URL"
chmod +x "$INSTALL_DIR/$BINARY_NAME"

mkdir -p "$CONFIG_DIR"
if [ ! -f "$CONFIG_DIR/config.yaml" ]; then
  echo "Downloading config.yaml.example..."
  curl -L -o "$CONFIG_DIR/config.yaml" "https://raw.githubusercontent.com/$REPO/main/config.yaml.example"
  echo "Created default config at $CONFIG_DIR/config.yaml. Please edit it before running!"
else
  echo "Config file already exists, skipping..."
fi

touch "$CONFIG_DIR/hosts.txt"
touch "$CONFIG_DIR/rule.txt"

echo "Creating Systemd service..."
cat <<EOF > /etc/systemd/system/$SERVICE_NAME.service
[Unit]
Description=DoH Automatic Traffic Splitting Service
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=$CONFIG_DIR
ExecStart=$INSTALL_DIR/$BINARY_NAME
Restart=always
RestartSec=5
Environment="DOH_AUTOPROXY_CONFIG=$CONFIG_DIR/config.yaml"

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable $SERVICE_NAME

echo "Installation complete!"
echo ""
echo "1. Edit config: nano $CONFIG_DIR/config.yaml"
echo "2. Start service: systemctl start $SERVICE_NAME"
echo "3. Check status: systemctl status $SERVICE_NAME"
