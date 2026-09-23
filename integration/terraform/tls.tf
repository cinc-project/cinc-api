# Everything secret is generated here, on the caller's machine, and kept only
# in local (gitignored) state and in .out/.

# Admin key: only the public half goes to the instance.
resource "tls_private_key" "admin" {
  algorithm = "RSA"
  rsa_bits  = 2048
}

# A throwaway CA the tests trust, and a server certificate for the Elastic IP
# signed by it, so TLS is fully verified without WithSkipTLSVerify. The server
# certificate's key reaches the instance through user-data, so anyone in the
# account who can read the instance's user-data can see it; it protects only
# this short-lived test server.
resource "tls_private_key" "ca" {
  algorithm = "RSA"
  rsa_bits  = 2048
}

resource "tls_self_signed_cert" "ca" {
  private_key_pem       = tls_private_key.ca.private_key_pem
  is_ca_certificate     = true
  validity_period_hours = 24 * 7
  allowed_uses          = ["cert_signing", "crl_signing"]

  subject {
    common_name  = "${var.name_prefix} test CA"
    organization = "cinc-api integration tests"
  }
}

resource "tls_private_key" "server" {
  algorithm = "RSA"
  rsa_bits  = 2048
}

resource "tls_cert_request" "server" {
  private_key_pem = tls_private_key.server.private_key_pem
  ip_addresses    = [aws_eip.server.public_ip]

  subject {
    common_name = aws_eip.server.public_ip
  }
}

resource "tls_locally_signed_cert" "server" {
  cert_request_pem      = tls_cert_request.server.cert_request_pem
  ca_private_key_pem    = tls_private_key.ca.private_key_pem
  ca_cert_pem           = tls_self_signed_cert.ca.cert_pem
  validity_period_hours = 24 * 7
  allowed_uses          = ["digital_signature", "key_encipherment", "server_auth"]
}

# Password for /_stats' HTTP Basic auth (user "statsuser").
resource "random_password" "stats" {
  length  = 32
  special = false
}
