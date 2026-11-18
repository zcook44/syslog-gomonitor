#!/bin/bash
# Generate a self-signed certificate or CSR for syslog-gomonitor HTTPS dashboard.
# Suitable for air-gapped/disconnected deployments.
set -euo pipefail

output_dir=""
cert_name="syslog-gomonitor"
days=3650
cn=""
alt_names=()
mode="self-signed"
deploy=0
force=0

usage() {
  cat <<'EOF'
Usage: generate-cert.sh [OPTIONS]

Generate a TLS certificate for the syslog-gomonitor HTTPS dashboard (port 8443).

MODES:
  --self-signed    Generate a self-signed certificate (default)
  --csr            Generate a Certificate Signing Request for an internal CA

OPTIONS:
  -o, --output DIR      Output directory for certificate files (default: ./tls)
  --cn COMMON_NAME      Common Name for the certificate (default: hostname)
  --san NAME|IP         Subject Alternative Name (can be repeated)
                        If not specified, includes hostname and non-loopback IPs
  --days DAYS           Certificate validity in days (default: 3650 = ~10 years)
                        Only applies to self-signed certificates
  --deploy              Install certificate to /etc/syslog-gomonitor/tls/
                        Requires root. Creates symlinks for install.sh compatibility.
  --force               Overwrite existing certificate files
  -h, --help            Show this help message

SELF-SIGNED MODE (default):
  Generates a self-signed certificate suitable for internal/air-gapped networks.
  Output files:
    - cert.pem          Server certificate (fullchain in self-signed case)
    - privkey.pem       Private key (mode 0600)
    - cert.conf         OpenSSL config used (for reference)

CSR MODE (--csr):
  Generates a Certificate Signing Request to submit to your internal CA.
  Output files:
    - csr.pem           Certificate Signing Request
    - privkey.pem       Private key (mode 0600, keep secure)
    - csr.conf          OpenSSL config used (for reference)

  After your CA signs the CSR, combine the signed certificate with any
  intermediate certificates into a fullchain file for install.sh:
    cat signed-cert.pem intermediate.pem > fullchain.pem

EXAMPLES:
  # Generate self-signed certificate with auto-detected hostnames/IPs
  sudo ./generate-cert.sh --self-signed --deploy

  # Generate self-signed certificate with specific SANs
  ./generate-cert.sh --self-signed -o /tmp/tls --san syslog.example.com --san 192.0.2.10

  # Generate CSR for internal CA
  ./generate-cert.sh --csr -o /tmp/tls --cn syslog.example.com --san syslog.example.com

  # Generate and deploy in one step (requires root)
  sudo ./generate-cert.sh --self-signed --deploy --cn syslog.internal.example.com

AFTER GENERATION:
  For self-signed certificates:
    install.sh --tls-cert /path/to/cert.pem --tls-key /path/to/privkey.pem

  Or if --deploy was used:
    install.sh --tls-cert /etc/syslog-gomonitor/tls/fullchain.pem \\
               --tls-key /etc/syslog-gomonitor/tls/privkey.pem

  For CSR mode:
    1. Submit csr.pem to your internal CA
    2. Receive signed certificate (and any intermediate certificates)
    3. Create fullchain: cat signed-cert.pem intermediate.pem > fullchain.pem
    4. Run: install.sh --tls-cert /path/to/fullchain.pem --tls-key /path/to/privkey.pem

SECURITY NOTES:
  - Private key is generated with mode 0600 (owner read/write only)
  - Self-signed certificates should be distributed to workstation trust stores
  - Keep privkey.pem secure and backed up
  - For production, consider using your organization's internal CA (--csr mode)

EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --self-signed) mode="self-signed"; shift ;;
    --csr) mode="csr"; shift ;;
    -o|--output)
      [[ $# -ge 2 ]] || { echo "generate-cert.sh: --output needs a directory" >&2; exit 2; }
      output_dir="$2"
      shift 2
      ;;
    --cn)
      [[ $# -ge 2 ]] || { echo "generate-cert.sh: --cn needs a name" >&2; exit 2; }
      cn="$2"
      shift 2
      ;;
    --san)
      [[ $# -ge 2 ]] || { echo "generate-cert.sh: --san needs a name or IP" >&2; exit 2; }
      alt_names+=("$2")
      shift 2
      ;;
    --days)
      [[ $# -ge 2 ]] || { echo "generate-cert.sh: --days needs a number" >&2; exit 2; }
      days="$2"
      shift 2
      ;;
    --deploy) deploy=1; shift ;;
    --force) force=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "generate-cert.sh: unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if ! command -v openssl >/dev/null 2>&1; then
  echo "generate-cert.sh: openssl is required" >&2
  exit 1
fi

if [[ "$deploy" -eq 1 && "$(id -u)" -ne 0 ]]; then
  echo "generate-cert.sh: --deploy requires root" >&2
  exit 1
fi

if [[ -z "$output_dir" ]]; then
  output_dir="./tls"
fi

if [[ -z "$cn" ]]; then
  cn="$(hostname -f 2>/dev/null || hostname)"
fi

mkdir -p "$output_dir"
chmod 750 "$output_dir"

privkey="$output_dir/privkey.pem"
if [[ "$mode" == "self-signed" ]]; then
  cert="$output_dir/cert.pem"
  config="$output_dir/cert.conf"
else
  cert="$output_dir/csr.pem"
  config="$output_dir/csr.conf"
fi

if [[ "$force" -eq 0 ]]; then
  if [[ -f "$privkey" || -f "$cert" ]]; then
    echo "generate-cert.sh: certificate files already exist in $output_dir" >&2
    echo "Use --force to overwrite, or choose a different --output directory" >&2
    exit 1
  fi
fi

collect_san() {
  local names=()
  
  # Add CN as first SAN
  names+=("$cn")
  
  # Add user-specified SANs
  for name in "${alt_names[@]}"; do
    names+=("$name")
  done
  
  # Auto-detect if no SANs were specified
  if [[ ${#alt_names[@]} -eq 0 ]]; then
    # Add localhost
    names+=("localhost")
    
    # Add all non-loopback IPv4 addresses
    local ips
    if command -v hostname >/dev/null 2>&1; then
      ips=$(hostname -I 2>/dev/null || true)
      for ip in $ips; do
        # Basic IPv4 check and skip loopback
        if [[ "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ && ! "$ip" =~ ^127\. ]]; then
          names+=("$ip")
        fi
      done
    fi
  fi
  
  # Return unique names
  printf '%s\n' "${names[@]}" | sort -u
}

san_names=($(collect_san))

# Generate OpenSSL config
cat > "$config" <<EOF
[req]
default_bits = 2048
prompt = no
default_md = sha256
distinguished_name = dn
req_extensions = v3_req

[dn]
CN = $cn

[v3_req]
keyUsage = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names

[v3_ca]
keyUsage = critical, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = @alt_names
basicConstraints = critical, CA:FALSE

[alt_names]
EOF

idx=1
for name in "${san_names[@]}"; do
  if [[ "$name" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "IP.$idx = $name" >> "$config"
  else
    echo "DNS.$idx = $name" >> "$config"
  fi
  ((idx++))
done

# Generate private key
echo "Generating private key..."
openssl genpkey -algorithm RSA -out "$privkey" -pkeyopt rsa_keygen_bits:2048 2>/dev/null
chmod 600 "$privkey"

if [[ "$mode" == "self-signed" ]]; then
  # Generate self-signed certificate
  echo "Generating self-signed certificate..."
  openssl req -new -x509 -key "$privkey" -out "$cert" -days "$days" -config "$config" -extensions v3_ca 2>/dev/null
  chmod 644 "$cert"
  
  echo ""
  echo "Self-signed certificate generated successfully!"
  echo ""
  echo "Output files:"
  echo "  Certificate: $cert"
  echo "  Private key: $privkey (mode 0600)"
  echo "  Config:      $config"
  echo ""
  echo "Certificate details:"
  openssl x509 -in "$cert" -noout -subject -issuer -dates -ext subjectAltName 2>/dev/null || \
    openssl x509 -in "$cert" -noout -subject -issuer -dates
  
  if [[ "$deploy" -eq 1 ]]; then
    echo ""
    echo "Deploying to /etc/syslog-gomonitor/tls/..."
    mkdir -p /etc/syslog-gomonitor/tls
    chmod 750 /etc/syslog-gomonitor/tls
    
    # For self-signed, cert.pem is the fullchain (no intermediates)
    ln -sf "$(realpath "$cert")" /etc/syslog-gomonitor/tls/fullchain.pem
    ln -sf "$(realpath "$privkey")" /etc/syslog-gomonitor/tls/privkey.pem
    
    echo "Certificate deployed. Run install.sh with:"
    echo "  sudo ./install.sh --tls-cert /etc/syslog-gomonitor/tls/fullchain.pem \\"
    echo "                    --tls-key /etc/syslog-gomonitor/tls/privkey.pem"
  else
    echo ""
    echo "To install, run:"
    echo "  sudo ./install.sh --tls-cert $(realpath "$cert") \\"
    echo "                    --tls-key $(realpath "$privkey")"
    echo ""
    echo "Workstations must trust this self-signed certificate."
    echo "Import $cert into browser/system trust stores."
  fi
  
else
  # Generate CSR
  echo "Generating Certificate Signing Request..."
  openssl req -new -key "$privkey" -out "$cert" -config "$config" 2>/dev/null
  chmod 644 "$cert"
  
  echo ""
  echo "Certificate Signing Request generated successfully!"
  echo ""
  echo "Output files:"
  echo "  CSR:         $cert"
  echo "  Private key: $privkey (mode 0600, KEEP SECURE)"
  echo "  Config:      $config"
  echo ""
  echo "CSR details:"
  openssl req -in "$cert" -noout -subject -text | grep -A1 "Subject Alternative Name" || \
    openssl req -in "$cert" -noout -subject
  echo ""
  echo "Next steps:"
  echo "  1. Submit $cert to your internal CA"
  echo "  2. Receive the signed certificate (and any intermediate certificates)"
  echo "  3. Create fullchain.pem: cat signed-cert.pem intermediate.pem > fullchain.pem"
  echo "  4. Install with: sudo ./install.sh --tls-cert /path/to/fullchain.pem \\"
  echo "                                      --tls-key $(realpath "$privkey")"
  echo ""
  echo "Keep $privkey secure. You'll need it when the CA returns the signed certificate."
  
  if [[ "$deploy" -eq 1 ]]; then
    echo ""
    echo "Note: --deploy with --csr only copies the private key."
    echo "You must manually place the CA-signed fullchain.pem after receiving it."
    mkdir -p /etc/syslog-gomonitor/tls
    chmod 750 /etc/syslog-gomonitor/tls
    ln -sf "$(realpath "$privkey")" /etc/syslog-gomonitor/tls/privkey.pem
    echo "Private key deployed to: /etc/syslog-gomonitor/tls/privkey.pem"
    echo "After CA signing, copy fullchain to: /etc/syslog-gomonitor/tls/fullchain.pem"
  fi
fi

echo ""
