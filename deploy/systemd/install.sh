#!/bin/bash
set -euo pipefail

if [[ $EUID != 0 || $# -lt 1 || $# -gt 2 || ( $# == 2 && $2 != --with-deploy-user ) ]]; then
  echo "Usage (Debian/Ubuntu amd64): sudo bash $0 https://homeatlas.example[:PORT] [--with-deploy-user]" >&2
  exit 1
fi
[[ $(uname -m) == x86_64 && $(uname -s) == Linux ]] || { echo 'Requires Linux amd64' >&2; exit 1; }
for command in python3 systemctl runuser sudo visudo; do
  command -v "$command" >/dev/null || { echo "Missing dependency: $command" >&2; exit 1; }
done
python3 -c 'import sys; sys.exit(sys.version_info < (3, 11))' || { echo 'Requires Python 3.11 or newer' >&2; exit 1; }
source_dir=$(cd -- "$(dirname -- "$0")" && pwd)
public_url=$(python3 - "$source_dir/homeatlas-deploy.py" "$1" <<'PY'
import runpy
import sys
print('https://' + runpy.run_path(sys.argv[1])['validate_origin'](sys.argv[2]))
PY
)

if ! id homeatlas >/dev/null 2>&1; then
  useradd --system --user-group --home-dir /var/lib/homeatlas --shell /usr/sbin/nologin homeatlas
fi
if [[ ${2:-} == --with-deploy-user ]]; then
  if ! id deploy >/dev/null 2>&1; then
    useradd --create-home --user-group --shell /bin/bash deploy
  fi
  # A dedicated account must not already have broader administrator access.
  if id -nG deploy | tr ' ' '\n' | /usr/bin/grep -Eq '^(sudo|wheel|admin)$'; then
    echo 'deploy must be a dedicated non-administrator account' >&2
    exit 1
  fi
fi

install -d -o root -g homeatlas -m 0750 /opt/homeatlas /opt/homeatlas/releases
install -d -o homeatlas -g homeatlas -m 0700 /var/lib/homeatlas
install -d -o root -g root -m 0700 /var/lib/homeatlas-deploy /etc/homeatlas
if [[ ! -e /etc/homeatlas/homeatlas.env ]]; then
  printf 'HOMEATLAS_PUBLIC_URL=%s\n' "$public_url" > /etc/homeatlas/homeatlas.env
  chmod 0600 /etc/homeatlas/homeatlas.env
else
  echo 'Preserving /etc/homeatlas/homeatlas.env; verify HOMEATLAS_PUBLIC_URL matches the VM.'
fi
install -o root -g root -m 0755 "$source_dir/homeatlas-deploy.py" /usr/local/sbin/homeatlas-deploy
install -o root -g root -m 0644 "$source_dir/homeatlas.service" /etc/systemd/system/homeatlas.service
if [[ ${2:-} == --with-deploy-user ]]; then
  sudoers=$(mktemp)
  trap 'rm -f "$sudoers"' EXIT
  printf 'deploy ALL=(root) NOPASSWD: /usr/local/sbin/homeatlas-deploy ""\n' > "$sudoers"
  visudo -cf "$sudoers"
  install -o root -g root -m 0440 "$sudoers" /etc/sudoers.d/homeatlas-deploy
fi
systemctl daemon-reload
systemctl enable homeatlas.service
echo 'Installed. Configure an HTTPS reverse proxy and deploy the first release to start HomeAtlas.'
