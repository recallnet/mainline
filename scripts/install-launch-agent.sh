#!/bin/sh
set -eu

label="com.recallnet.mainline.global"
interval="2s"
binary=""
registry=""

while [ $# -gt 0 ]; do
  case "$1" in
    --binary)
      binary="$2"
      shift 2
      ;;
    --interval)
      interval="$2"
      shift 2
      ;;
    --registry)
      registry="$2"
      shift 2
      ;;
    *)
      echo "usage: $0 [--binary /path/to/mainlined] [--interval 2s] [--registry /path/to/registry.json]" >&2
      exit 2
      ;;
  esac
done

if [ -z "$binary" ]; then
  binary="$(command -v mainlined || true)"
fi

if [ -z "$binary" ]; then
  echo "mainlined not found on PATH; pass --binary /path/to/mainlined" >&2
  exit 1
fi

uid="$(id -u)"
launch_dir="$HOME/Library/LaunchAgents"
log_dir="$HOME/Library/Logs/mainline"
plist_path="$launch_dir/$label.plist"

mkdir -p "$launch_dir" "$log_dir"

registry_xml=""
if [ -n "$registry" ]; then
  registry_xml="    <string>--registry</string>
    <string>$registry</string>"
fi

cat >"$plist_path" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$label</string>
  <key>ProgramArguments</key>
  <array>
    <string>$binary</string>
    <string>--all</string>
    <string>--json</string>
    <string>--interval</string>
    <string>$interval</string>
$registry_xml
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>$log_dir/mainlined.out.log</string>
  <key>StandardErrorPath</key>
  <string>$log_dir/mainlined.err.log</string>
</dict>
</plist>
EOF

launchctl bootout "gui/$uid/$label" >/dev/null 2>&1 || true
launchctl bootstrap "gui/$uid" "$plist_path"
launchctl kickstart -k "gui/$uid/$label"

echo "Installed launch agent: $plist_path"
echo "Binary: $binary"
echo "Logs: $log_dir"
echo "Verify: launchctl print gui/$uid/$label"
