#!/usr/bin/env bash
# Copyright (c) 2026 Boggy Creek Software LLC
#
# Use of this source code is governed by an MIT-style
# license that can be found in the LICENSE file.

# Fleet Installer
#
# Supports macOS (Darwin) and Linux (all standard distributions).
#
# Can be executed via curl from GitHub:
#   curl -fsSL https://raw.githubusercontent.com/boggycreek/fleet/main/install.sh | bash
#
# Or run directly from a cloned repository checkout:
#   ./install.sh
#
# Options:
#   --remote <url>    Override the git remote repository URL to clone from.
#   --branch <branch> Specify a git branch or tag (default: main).
#   -h, --help        Show usage information.

set -euo pipefail

# Default configuration
REPO_URL_DEFAULT="https://github.com/boggycreek/fleet.git"
DEFAULT_BRANCH="main"

DATA_HOME="${XDG_DATA_HOME:-${HOME}/.local/share}/fleet"
REPO_DIR="${DATA_HOME}/repo"
BIN_DIR="${HOME}/.local/bin"
SECRETS_DIR="${DATA_HOME}/secrets"

REMOTE_URL="${FLEET_REMOTE:-${AGENT_SANDBOX_REMOTE:-${REPO_URL_DEFAULT}}}"
BRANCH="${FLEET_BRANCH:-${AGENT_SANDBOX_BRANCH:-${DEFAULT_BRANCH}}}"

print_usage() {
  cat <<'EOF'
Usage: install.sh [options]

Options:
  --remote <url>     Override repository remote URL
  --branch <branch>  Specify repository branch/tag to checkout (default: main)
  -h, --help         Show this help message
EOF
}

# Parse command line arguments
while [ $# -gt 0 ]; do
  case "$1" in
    --remote)
      REMOTE_URL="$2"
      shift 2
      ;;
    --branch)
      BRANCH="$2"
      shift 2
      ;;
    -h|--help)
      print_usage
      exit 0
      ;;
    *)
      echo "Error: Unknown option: $1" >&2
      print_usage >&2
      exit 1
      ;;
  esac
done

echo "========================================"
echo "           Fleet Installer              "
echo "========================================"
echo

# 1. Operating System & Architecture Detection
echo "[1/6] Detecting operating system and hardware platform..."
OS_TYPE="$(uname -s)"
ARCH_TYPE="$(uname -m)"

case "${OS_TYPE}" in
  Darwin)
    OS="darwin"
    OS_PRETTY="macOS"
    ;;
  Linux)
    OS="linux"
    if [ -f /etc/os-release ]; then
      # Source os-release for pretty distro name
      # shellcheck disable=SC1091
      DISTRO_NAME="$(. /etc/os-release && echo "${PRETTY_NAME:-Linux}")"
      OS_PRETTY="Linux (${DISTRO_NAME})"
    else
      OS_PRETTY="Linux"
    fi
    ;;
  *)
    echo "Error: Unsupported operating system: ${OS_TYPE}." >&2
    echo "Fleet currently supports macOS (Darwin) and Linux." >&2
    exit 1
    ;;
esac

case "${ARCH_TYPE}" in
  x86_64|amd64)
    ARCH="amd64"
    ;;
  arm64|aarch64)
    ARCH="arm64"
    ;;
  *)
    echo "Error: Unsupported CPU architecture: ${ARCH_TYPE}." >&2
    echo "Supported architectures: x86_64 / amd64, arm64 / aarch64." >&2
    exit 1
    ;;
esac

echo "  Platform: ${OS_PRETTY} [${OS}/${ARCH}]"

# 2. Preflight Dependency Checks
echo
echo "[2/6] Checking host prerequisites..."
MISSING_TOOLS=()

for tool in git curl ssh-keygen podman; do
  if ! command -v "${tool}" >/dev/null 2>&1; then
    MISSING_TOOLS+=("${tool}")
  fi
done

if [ ${#MISSING_TOOLS[@]} -ne 0 ]; then
  echo "Error: The following required utilities are missing: ${MISSING_TOOLS[*]}" >&2
  if [ "${OS}" = "darwin" ]; then
    echo "On macOS:" >&2
    echo "  - Install Xcode Command Line Tools: xcode-select --install" >&2
    echo "  - Install Podman: brew install podman (or Podman Desktop)" >&2
  elif [ "${OS}" = "linux" ]; then
    echo "On Linux, install via your package manager:" >&2
    echo "  - Ubuntu/Debian: sudo apt update && sudo apt install -y git curl openssh-client podman" >&2
    echo "  - Fedora/RHEL:   sudo dnf install -y git curl openssh podman" >&2
    echo "  - Arch Linux:    sudo pacman -S --needed git curl openssh podman" >&2
  fi
  exit 1
fi

CONTAINER_ENGINE="podman"
echo "  Container engine: podman ($(${CONTAINER_ENGINE} --version | head -n1))"

# Detect Go Toolchain
if command -v go >/dev/null 2>&1; then
  echo "  Go compiler detected: $(go version)"
else
  echo "  Note: Go compiler not found on PATH. Required for building native binaries from source."
fi

# 3. Setup XDG Directories
echo
echo "[3/6] Configuring XDG directories..."
mkdir -p "${BIN_DIR}"
mkdir -p "${DATA_HOME}"
mkdir -p "${SECRETS_DIR}"

echo "  Binaries directory : ${BIN_DIR}"
echo "  Data directory     : ${DATA_HOME}"

# 4. Sync / Resolve Repository Location
echo
echo "[4/6] Setting up fleet repository..."

# Determine if we are running from an existing local clone or via piped curl
CURRENT_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-}")" 2>/dev/null && pwd || echo "")"

if [ -n "${CURRENT_SCRIPT_DIR}" ] && [ -d "${CURRENT_SCRIPT_DIR}/.git" ]; then
  # Executing directly from within a local git repository clone
  FLEET_ROOT="${CURRENT_SCRIPT_DIR}"
  echo "  Running from local repository checkout at: ${FLEET_ROOT}"
else
  # Executing via curl pipe or outside a repository checkout -> ensure standard XDG checkout
  if [ -d "${REPO_DIR}/.git" ]; then
    echo "  Existing repository checkout found at ${REPO_DIR}. Syncing..."
    if ! git -C "${REPO_DIR}" diff --quiet 2>/dev/null || ! git -C "${REPO_DIR}" diff --cached --quiet 2>/dev/null; then
      echo "  Warning: ${REPO_DIR} has local modifications. Skipping git pull to preserve changes."
    else
      git -C "${REPO_DIR}" fetch origin "${BRANCH}" 2>/dev/null || true
      git -C "${REPO_DIR}" checkout "${BRANCH}" 2>/dev/null || true
      git -C "${REPO_DIR}" pull --ff-only origin "${BRANCH}" 2>/dev/null || echo "  Warning: git pull failed. Continuing with existing checkout."
    fi
  else
    echo "  Cloning ${REMOTE_URL} (branch: ${BRANCH}) into ${REPO_DIR}..."
    git clone --branch "${BRANCH}" "${REMOTE_URL}" "${REPO_DIR}"
  fi
  FLEET_ROOT="${REPO_DIR}"
fi
SANDBOX_ROOT="${FLEET_ROOT}"

# 5. Generate Dedicated IDE SSH Keypair
echo
echo "[5/6] Ensuring dedicated IDE SSH keypair..."
SSH_KEY="${HOME}/.ssh/fleet"
LEGACY_SSH_KEY="${HOME}/.ssh/agent-sandbox"
mkdir -p "${HOME}/.ssh"
chmod 700 "${HOME}/.ssh"

if [ ! -f "${SSH_KEY}" ]; then
  if [ -f "${LEGACY_SSH_KEY}" ]; then
    echo "  Migrating existing IDE keypair from ${LEGACY_SSH_KEY} to ${SSH_KEY}"
    cp -p "${LEGACY_SSH_KEY}" "${SSH_KEY}"
    if [ -f "${LEGACY_SSH_KEY}.pub" ]; then
      cp -p "${LEGACY_SSH_KEY}.pub" "${SSH_KEY}.pub"
    fi
    chmod 600 "${SSH_KEY}"
    chmod 644 "${SSH_KEY}.pub" 2>/dev/null || true
  else
    echo "  Generating dedicated IDE keypair: ${SSH_KEY} (ed25519)"
    ssh-keygen -t ed25519 -N "" -C "fleet-ide" -f "${SSH_KEY}" >/dev/null
    chmod 600 "${SSH_KEY}"
    chmod 644 "${SSH_KEY}.pub"
  fi
else
  echo "  Existing IDE keypair found at ${SSH_KEY}"
fi

# Ensure managed ssh_config file exists
MANAGED_SSH_CONFIG="${DATA_HOME}/ssh_config"
if [ ! -f "${MANAGED_SSH_CONFIG}" ]; then
  cat > "${MANAGED_SSH_CONFIG}" <<'EOF'
# Fleet Auto-Generated SSH Configuration
# Managed automatically by fleet. Do not edit manually.
EOF
  chmod 600 "${MANAGED_SSH_CONFIG}"
fi

# Configure Include in ~/.ssh/config if not already present
SSH_USER_CONFIG="${HOME}/.ssh/config"
INCLUDE_DIRECTIVE="Include ${MANAGED_SSH_CONFIG}"
if [ -f "${SSH_USER_CONFIG}" ]; then
  if ! grep -qF "${MANAGED_SSH_CONFIG}" "${SSH_USER_CONFIG}"; then
    echo "  Adding Include directive for Fleet to ${SSH_USER_CONFIG}"
    TMP_CONFIG="$(umask 077 && mktemp "${TMPDIR:-/tmp}/fleet-ssh.XXXXXX")"
    {
      echo "# Fleet Host Include"
      echo "${INCLUDE_DIRECTIVE}"
      echo ""
      cat "${SSH_USER_CONFIG}"
    } > "${TMP_CONFIG}"
    chmod 600 "${TMP_CONFIG}"
    mv "${TMP_CONFIG}" "${SSH_USER_CONFIG}"
    chmod 600 "${SSH_USER_CONFIG}"
  fi
else
  echo "  Creating ${SSH_USER_CONFIG} with Fleet Include directive"
  (
    umask 077
    {
      echo "# OpenSSH Configuration"
      echo "# Fleet Host Include"
      echo "${INCLUDE_DIRECTIVE}"
    } > "${SSH_USER_CONFIG}"
  )
  chmod 600 "${SSH_USER_CONFIG}"
fi

# 6. Initialize Environment and CLI Entrypoint
echo
echo "[6/6] Initializing configuration and host CLI..."

ENV_FILE="${DATA_HOME}/.env"
if [ ! -f "${ENV_FILE}" ]; then
  echo "  Creating initial configuration at ${ENV_FILE}"
  HUMAN_NAME="${USER:-operator}"

  # Generate random 32-character hex secret portably across macOS and Linux
  if command -v openssl >/dev/null 2>&1; then
    ADMIN_PW="$(openssl rand -hex 16)"
    HUMAN_PW="$(openssl rand -hex 16)"
  else
    ADMIN_PW="$(od -vN 16 -An -tx1 /dev/urandom | tr -d ' \n')"
    HUMAN_PW="$(od -vN 16 -An -tx1 /dev/urandom | tr -d ' \n')"
  fi

  (
    umask 077
    cat > "${ENV_FILE}" <<EOF
# Fleet Environment Configuration
# Generated for ${OS_PRETTY} (${OS}/${ARCH})

FLEET_ROOT=${FLEET_ROOT}
SANDBOX_ROOT=${FLEET_ROOT}
HUMAN_NAME=${HUMAN_NAME}
ADMIN_BACKPLANE_PASSWORD=${ADMIN_PW}
HUMAN_BACKPLANE_PASSWORD=${HUMAN_PW}
SONAR_ADMIN_USER=admin
SONAR_ADMIN_PASSWORD=Admin_${ADMIN_PW}

# Optional API Keys (uncomment and populate as needed)
# ANTHROPIC_API_KEY=
# OPENAI_API_KEY=
# GH_TOKEN=
EOF
  )
  chmod 600 "${ENV_FILE}"
else
  echo "  Existing configuration file found at ${ENV_FILE}"
  # Ensure FLEET_ROOT and SANDBOX_ROOT are recorded if not present
  if ! grep -q "^FLEET_ROOT=" "${ENV_FILE}" 2>/dev/null; then
    echo "FLEET_ROOT=${FLEET_ROOT}" >> "${ENV_FILE}"
  fi
  if ! grep -q "^SANDBOX_ROOT=" "${ENV_FILE}" 2>/dev/null; then
    echo "SANDBOX_ROOT=${FLEET_ROOT}" >> "${ENV_FILE}"
  fi
  if ! grep -q "^SONAR_ADMIN_PASSWORD=" "${ENV_FILE}" 2>/dev/null; then
    # Derive from existing ADMIN_BACKPLANE_PASSWORD if available
    EXISTING_ADMIN_PW="$(grep "^ADMIN_BACKPLANE_PASSWORD=" "${ENV_FILE}" 2>/dev/null | cut -d= -f2 || true)"
    if [ -n "${EXISTING_ADMIN_PW}" ]; then
      echo "SONAR_ADMIN_USER=admin" >> "${ENV_FILE}"
      echo "SONAR_ADMIN_PASSWORD=Admin_${EXISTING_ADMIN_PW}" >> "${ENV_FILE}"
    fi
  fi
fi

# Configure fleet, bp, bpd, and sonar-mcp CLI executables
FLEET_BIN="${BIN_DIR}/fleet"
FT_BIN="${BIN_DIR}/ft"
BP_BIN="${BIN_DIR}/bp"
BPD_BIN="${BIN_DIR}/bpd"
SONAR_MCP_BIN="${BIN_DIR}/sonar-mcp"

INSTALLED_PREBUILT=true
RELEASE_URL_BASE="https://github.com/boggycreek/fleet/releases/latest/download"

echo "  Checking for prebuilt native CLI binaries from GitHub Releases..."
CHECKSUM_FILE="$(umask 077 && mktemp "${TMPDIR:-/tmp}/fleet-checksums.XXXXXX")"
if curl -fsSL "${RELEASE_URL_BASE}/checksums.txt" -o "${CHECKSUM_FILE}" 2>/dev/null; then
  for bin_name in fleet bp bpd sonar-mcp; do
    target_file="${BIN_DIR}/${bin_name}"
    remote_asset="${bin_name}-${OS}-${ARCH}"
    download_url="${RELEASE_URL_BASE}/${remote_asset}"

    expected_sha256="$(grep -E "(^| )${remote_asset}\$" "${CHECKSUM_FILE}" | awk '{print $1}' || echo "")"
    if [ -z "${expected_sha256}" ]; then
      echo "  Warning: Checksum for ${remote_asset} not found in release checksums.txt"
      INSTALLED_PREBUILT=false
      break
    fi

    if curl -fsSL "${download_url}" -o "${target_file}.tmp" 2>/dev/null; then
      if command -v sha256sum >/dev/null 2>&1; then
        actual_sha256="$(sha256sum "${target_file}.tmp" | awk '{print $1}')"
      elif command -v shasum >/dev/null 2>&1; then
        actual_sha256="$(shasum -a 256 "${target_file}.tmp" | awk '{print $1}')"
      else
        actual_sha256=""
      fi

      if [ -n "${actual_sha256}" ] && [ "${actual_sha256}" = "${expected_sha256}" ]; then
        mv "${target_file}.tmp" "${target_file}"
        chmod +x "${target_file}"
      else
        echo "  Warning: Checksum mismatch for ${remote_asset} (expected ${expected_sha256}, got ${actual_sha256})"
        rm -f "${target_file}.tmp"
        INSTALLED_PREBUILT=false
        break
      fi
    else
      rm -f "${target_file}.tmp"
      INSTALLED_PREBUILT=false
      break
    fi
  done
else
  INSTALLED_PREBUILT=false
fi
rm -f "${CHECKSUM_FILE}"

if [ "${INSTALLED_PREBUILT}" = "true" ] && [ -f "${FLEET_BIN}" ]; then
  echo "  Installed prebuilt native CLI binaries (fleet, bp, bpd, sonar-mcp) from GitHub Releases."
elif command -v go >/dev/null 2>&1 && { [ -f "${SANDBOX_ROOT}/go.work" ] || [ -f "${SANDBOX_ROOT}/go.mod" ]; }; then
  echo "  Compiling native Go CLI binaries from source..."
  (cd "${SANDBOX_ROOT}" && env -u GOROOT go build -trimpath -ldflags="-s -w" -o "${FLEET_BIN}" ./cli/cmd/fleet && env -u GOROOT go build -trimpath -ldflags="-s -w" -o "${BP_BIN}" ./backplane/cmd/bp && env -u GOROOT go build -trimpath -ldflags="-s -w" -o "${BPD_BIN}" ./backplane/cmd/bpd && env -u GOROOT go build -trimpath -ldflags="-s -w" -o "${SONAR_MCP_BIN}" ./mcp/cmd/sonar-mcp)
  chmod +x "${FLEET_BIN}" "${BP_BIN}" "${BPD_BIN}" "${SONAR_MCP_BIN}" 2>/dev/null || true
  echo "  Installed ${FLEET_BIN}, ${BP_BIN}, ${BPD_BIN}, and ${SONAR_MCP_BIN}"
elif [ ! -f "${FLEET_BIN}" ]; then
  cat > "${FLEET_BIN}" <<EOF
#!/usr/bin/env bash
# Temporary bootstrap dispatcher until native Go binary is compiled or downloaded
if command -v go >/dev/null 2>&1 && [ -d "${SANDBOX_ROOT}" ]; then
  echo "Compiling native fleet binary..."
  (cd "${SANDBOX_ROOT}" && env -u GOROOT go build -trimpath -ldflags="-s -w" -o "${FLEET_BIN}" ./cli/cmd/fleet && env -u GOROOT go build -trimpath -ldflags="-s -w" -o "${BP_BIN}" ./backplane/cmd/bp)
  exec "${FLEET_BIN}" "\$@"
fi
echo "Fleet CLI (fleet)"
echo "Go compiler or prebuilt release required. Visit: https://github.com/boggycreek/fleet/releases"
exit 1
EOF
  chmod +x "${FLEET_BIN}"
  echo "  Installed bootstrap script ${FLEET_BIN}"
fi

# Ensure relative symlink ft -> fleet exists safely if fleet exists, but skip if another ft command exists on operator's system
if [ -f "${FLEET_BIN}" ]; then
  existing_ft="$(command -v ft 2>/dev/null || true)"
  if [ -n "${existing_ft}" ] && [ "${existing_ft}" != "${FT_BIN}" ]; then
    echo "  Skipping 'ft' alias: another 'ft' command was found on the system (${existing_ft})"
  elif [ ! -e "${FT_BIN}" ] && [ ! -L "${FT_BIN}" ]; then
    (cd "${BIN_DIR}" && ln -s fleet ft)
  elif [ -L "${FT_BIN}" ]; then
    link_target=$(readlink "${FT_BIN}" 2>/dev/null || true)
    if [ "${link_target}" = "fleet" ] || [ "$(basename "${link_target}")" = "fleet" ]; then
      (cd "${BIN_DIR}" && ln -sf fleet ft)
    else
      echo "  Skipping 'ft' alias: ${FT_BIN} links to ${link_target}"
    fi
  else
    echo "  Skipping 'ft' alias: ${FT_BIN} is an existing regular file"
  fi
fi

echo
echo "========================================"
echo "       Installation Succeeded!         "
echo "========================================"
echo
echo "Target Platform     : ${OS_PRETTY} [${OS}/${ARCH}]"
echo "Repository Location : ${FLEET_ROOT}"
echo "Environment Config  : ${ENV_FILE}"
echo "CLI Executable      : ${FLEET_BIN} (alias: ${FT_BIN})"
echo "IDE SSH Public Key  : ${SSH_KEY}.pub"
echo

# Determine user's active shell configuration file
USER_SHELL="$(basename "${SHELL:-bash}")"
case "${USER_SHELL}" in
  zsh)
    SHELL_RC="${HOME}/.zshrc"
    ;;
  bash)
    if [ "${OS}" = "darwin" ]; then
      SHELL_RC="${HOME}/.bash_profile"
    else
      SHELL_RC="${HOME}/.bashrc"
    fi
    ;;
  *)
    SHELL_RC="${HOME}/.profile"
    ;;
esac

# PATH verification reminder
case ":${PATH}:" in
  *:"${BIN_DIR}":*) ;;
  *)
    echo "Notice: ${BIN_DIR} is not currently in your \$PATH."
    echo "Add it by running the following command:"
    echo
    echo "  echo 'export PATH=\"${BIN_DIR}:\$PATH\"' >> \"${SHELL_RC}\""
    echo "  source \"${SHELL_RC}\""
    echo
    ;;
esac

echo "Next steps:"
echo "  1. (Optional) Edit API keys in: ${ENV_FILE}"
echo "  2. Navigate to repo: cd \"${SANDBOX_ROOT}\""
echo "  3. Build binaries: make build-cli"
echo
